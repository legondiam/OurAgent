package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/repository"
	"OurAgent/internal/vectorstore"

	"github.com/cloudwego/eino/components/embedding"
	"gorm.io/gorm"
)

type memoryConsolidator interface {
	Consolidate(context.Context, string, []uint64) error
}

type MemoryConsumer struct {
	queue        taskConsumerQueue
	repo         *repository.LongTermMemoryRepository
	vectors      *vectorstore.MemoryQdrantStore
	embedder     embedding.Embedder
	consolidator memoryConsolidator
	cfg          config.Config
	modelName    string
}

// NewMemoryConsumer 创建长期记忆异步任务消费者
func NewMemoryConsumer(q taskConsumerQueue, repo *repository.LongTermMemoryRepository, vectors *vectorstore.MemoryQdrantStore, embedder embedding.Embedder, consolidator memoryConsolidator, cfg *config.Config) *MemoryConsumer {
	return &MemoryConsumer{queue: q, repo: repo, vectors: vectors, embedder: embedder, consolidator: consolidator, cfg: *cfg, modelName: cfg.LLM.EmbeddingModel}
}

// Start 启动归并、索引和删除任务消费者
func (c *MemoryConsumer) Start(ctx context.Context) error {
	if !c.cfg.LongTermMemory.Enabled {
		return nil
	}
	if err := c.startWorkers(ctx, c.cfg.Rabbit.MemoryConsolidateQueue, c.cfg.Rabbit.MemoryConsolidateWorkers, c.handleConsolidate); err != nil {
		return err
	}
	if err := c.startWorkers(ctx, c.cfg.Rabbit.MemoryIndexQueue, c.cfg.Rabbit.MemoryIndexWorkers, c.handleIndex); err != nil {
		return err
	}
	return c.startWorkers(ctx, c.cfg.Rabbit.MemoryDeleteQueue, c.cfg.Rabbit.MemoryDeleteWorkers, c.handleDelete)
}

func (c *MemoryConsumer) startWorkers(ctx context.Context, name string, count int, handler func(context.Context, queue.Delivery)) error {
	if count <= 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		if err := c.queue.Consume(ctx, name, c.cfg.Rabbit.PrefetchCount, handler); err != nil {
			return err
		}
	}
	return nil
}

// handleConsolidate 处理候选记忆批量归并任务
func (c *MemoryConsumer) handleConsolidate(ctx context.Context, delivery queue.Delivery) {
	msg, job, ok := c.claim(ctx, delivery)
	if !ok {
		return
	}
	var payload struct {
		SignalIDs []uint64 `json:"signal_ids"`
	}
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		c.fail(ctx, delivery, msg, err, queue.MemoryConsolidateRetryRoutingKey, queue.MemoryConsolidateDLQRoutingKey)
		return
	}
	if c.consolidator == nil {
		c.fail(ctx, delivery, msg, fmt.Errorf("记忆归并器未配置"), queue.MemoryConsolidateRetryRoutingKey, queue.MemoryConsolidateDLQRoutingKey)
		return
	}
	if err := c.consolidator.Consolidate(ctx, job.ID, payload.SignalIDs); err != nil {
		c.fail(ctx, delivery, msg, err, queue.MemoryConsolidateRetryRoutingKey, queue.MemoryConsolidateDLQRoutingKey)
		return
	}
	_ = c.repo.CompleteSignals(job.ID)
	_ = c.repo.CompleteJob(job.ID)
	_ = delivery.Ack()
}

// handleIndex 处理长期记忆向量索引任务
func (c *MemoryConsumer) handleIndex(ctx context.Context, delivery queue.Delivery) {
	msg, job, ok := c.claim(ctx, delivery)
	if !ok {
		return
	}
	if job.MemoryID == nil {
		c.fail(ctx, delivery, msg, fmt.Errorf("索引任务缺少memory_id"), queue.MemoryIndexRetryRoutingKey, queue.MemoryIndexDLQRoutingKey)
		return
	}
	memory, err := c.repo.MemoryForIndex(*job.MemoryID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			_ = c.repo.CompleteJob(job.ID)
			_ = delivery.Ack()
			return
		}
		c.fail(ctx, delivery, msg, err, queue.MemoryIndexRetryRoutingKey, queue.MemoryIndexDLQRoutingKey)
		return
	}
	if memory.Status != model.MemoryStatusActive && memory.Status != model.MemoryStatusCandidate {
		_ = c.repo.CompleteJob(job.ID)
		_ = delivery.Ack()
		return
	}
	text := fmt.Sprintf("记忆类型：%s\n主体：%s\n属性：%s\n内容：%s", memory.Type, memory.Subject, memory.Attribute, memory.Content)
	vectors, err := c.embedder.EmbedStrings(ctx, []string{text})
	if err != nil || len(vectors) == 0 {
		if err == nil {
			err = fmt.Errorf("Embedding返回空结果")
		}
		c.fail(ctx, delivery, msg, err, queue.MemoryIndexRetryRoutingKey, queue.MemoryIndexDLQRoutingKey)
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(c.modelName+"\x00"+text)))
	payload := map[string]any{"user_id": memory.UserID, "scope": memory.Scope, "memory_type": memory.Type, "status": memory.Status}
	if memory.KnowledgeBaseID != nil {
		payload["knowledge_base_id"] = *memory.KnowledgeBaseID
	}
	if err := c.vectors.EnsureCollection(ctx, len(vectors[0])); err == nil {
		err = c.vectors.Upsert(ctx, memory.ID, memory.Version, vectors[0], payload)
	}
	if err != nil {
		c.fail(ctx, delivery, msg, err, queue.MemoryIndexRetryRoutingKey, queue.MemoryIndexDLQRoutingKey)
		return
	}
	if !c.repo.IsMemoryIndexCurrent(memory.ID, memory.Version) {
		_ = c.vectors.Delete(ctx, memory.ID)
		_ = c.repo.CompleteJob(job.ID)
		_ = delivery.Ack()
		return
	}
	_ = c.repo.MarkIndexed(memory.ID, memory.Version, c.modelName, hash)
	_ = c.repo.CompleteJob(job.ID)
	_ = delivery.Ack()
}

// handleDelete 处理长期记忆向量清理和彻底删除任务
func (c *MemoryConsumer) handleDelete(ctx context.Context, delivery queue.Delivery) {
	msg, job, ok := c.claim(ctx, delivery)
	if !ok {
		return
	}
	if job.MemoryID == nil {
		c.fail(ctx, delivery, msg, fmt.Errorf("删除任务缺少memory_id"), queue.MemoryDeleteRetryRoutingKey, queue.MemoryDeleteDLQRoutingKey)
		return
	}
	if job.Type == model.MemoryJobExpire && !c.repo.IsMemoryExpired(*job.MemoryID) {
		_ = c.repo.CompleteJob(job.ID)
		_ = delivery.Ack()
		return
	}
	if err := c.vectors.Delete(ctx, *job.MemoryID); err != nil {
		c.fail(ctx, delivery, msg, err, queue.MemoryDeleteRetryRoutingKey, queue.MemoryDeleteDLQRoutingKey)
		return
	}
	if job.Type == model.MemoryJobExpire {
		_ = c.repo.CompleteJob(job.ID)
		_ = delivery.Ack()
		return
	}
	if err := c.repo.CompleteDelete(*job.MemoryID, job.UserID); err != nil {
		c.fail(ctx, delivery, msg, err, queue.MemoryDeleteRetryRoutingKey, queue.MemoryDeleteDLQRoutingKey)
		return
	}
	_ = c.repo.CompleteJob(job.ID)
	_ = delivery.Ack()
}

func (c *MemoryConsumer) claim(ctx context.Context, delivery queue.Delivery) (MemoryJobMessage, *model.LongTermMemoryJob, bool) {
	var msg MemoryJobMessage
	if err := json.Unmarshal(delivery.Body, &msg); err != nil {
		_ = delivery.Ack()
		return msg, nil, false
	}
	job, err := c.repo.FindJob(msg.JobID)
	if err != nil {
		_ = delivery.Ack()
		return msg, nil, false
	}
	lease := c.cfg.LongTermMemory.TaskLeaseSeconds
	if lease <= 0 {
		lease = 180
	}
	claimed, err := c.repo.ClaimJob(job.ID, time.Now(), time.Now().Add(time.Duration(lease)*time.Second))
	if err != nil || !claimed {
		_ = delivery.Ack()
		return msg, nil, false
	}
	job, err = c.repo.FindJob(job.ID)
	if err != nil {
		_ = delivery.Nack(true)
		return msg, nil, false
	}
	return msg, job, true
}

func (c *MemoryConsumer) fail(ctx context.Context, delivery queue.Delivery, msg MemoryJobMessage, cause error, retryKey, dlqKey string) {
	_ = c.repo.FailJob(msg.JobID, cause.Error())
	msg.Attempt++
	publishRetry(ctx, c.queue, delivery, c.cfg.Rabbit.MaxRetries, retryKey, dlqKey, msg, msg.Attempt, cause, "长期记忆任务执行失败")
}
