package tasks

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/repository"
	appsearch "OurAgent/internal/search"
	"OurAgent/pkg/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type ConsumerOptions struct {
	MaxRetries       int
	Prefetch         int
	IndexWorkers     int
	IndexLease       time.Duration
	DeleteWorkers    int
	SourceSyncWorker int
}

type IndexConsumer struct {
	queue   taskConsumerQueue
	docs    taskIndexDocumentRepository
	sources taskSourceRepository
	indexer taskIndexer
	opts    ConsumerOptions
}

type DeleteConsumer struct {
	queue   taskConsumerQueue
	docs    taskDeleteDocumentRepository
	chunks  taskChunkRepository
	qdrant  taskVectorStore
	keyword appsearch.KeywordStore
	minio   taskObjectStore
	sources taskSourceRepository
	opts    ConsumerOptions
}

type taskConsumerQueue interface {
	Consume(ctx context.Context, queueName string, prefetch int, handler func(context.Context, queue.Delivery)) error
	PublishJSON(ctx context.Context, routingKey string, value any) error
}

type taskIndexDocumentRepository interface {
	FindByIDAndUserID(id, userID uint64) (*model.Document, error)
	ClaimDocumentIndex(id, userID uint64, taskID string, attempt int, now, leaseUntil time.Time) (bool, error)
	RequeueDocumentIndex(id, userID uint64, taskID string, attempt int, message string) (bool, error)
	FinalizeDocumentIndexFailure(id, userID uint64, taskID string, attempt int, message string) (bool, error)
}

type taskDeleteDocumentRepository interface {
	FindByIDAndUserID(id, userID uint64) (*model.Document, error)
	DeleteByIDAndUserID(id, userID uint64) error
	MarkDeindexing(id, userID uint64) (bool, error)
	UpdateStatusIfCurrent(id, userID uint64, currentStatus, status, message string, chunkCount int) (bool, error)
}

type taskSourceRepository interface {
	MarkExternalDocumentSynced(id, documentID uint64) error
	MarkExternalDocumentDeindexed(id, documentID uint64) error
	MarkExternalDocumentFailed(id, documentID uint64, message string) error
	MarkExternalDocumentDeleted(id, documentID uint64) error
}

type taskChunkRepository interface {
	DeleteByDocumentID(userID, documentID uint64) error
}

type taskIndexer interface {
	Index(ctx context.Context, documentID uint64, taskID string) error
}

type taskVectorStore interface {
	DeleteByDocument(ctx context.Context, userID, knowledgeBaseID, documentID uint64) error
}

type taskObjectStore interface {
	DeleteObject(ctx context.Context, objectKey string) error
}

var (
	errIndexLeaseActive = stderrors.New("文档索引租约仍然有效")
	errRedriveIndexDLQ  = stderrors.New("重新投递文档索引死信")
)

func NewIndexConsumer(q taskConsumerQueue, docs taskIndexDocumentRepository, sources taskSourceRepository, indexer taskIndexer, cfg config.RabbitMQConfig) *IndexConsumer {
	return &IndexConsumer{
		queue:   q,
		docs:    docs,
		sources: sources,
		indexer: indexer,
		opts:    optionsFromConfig(cfg),
	}
}

func NewDeleteConsumer(q taskConsumerQueue, docs taskDeleteDocumentRepository, sources taskSourceRepository, chunks taskChunkRepository, qdrant taskVectorStore, keyword appsearch.KeywordStore, minio taskObjectStore, cfg config.RabbitMQConfig) *DeleteConsumer {
	return &DeleteConsumer{
		queue:   q,
		docs:    docs,
		sources: sources,
		chunks:  chunks,
		qdrant:  qdrant,
		keyword: keyword,
		minio:   minio,
		opts:    optionsFromConfig(cfg),
	}
}

func (c *IndexConsumer) Start(ctx context.Context, queueName string) error {
	for i := 0; i < c.opts.IndexWorkers; i++ {
		if err := c.queue.Consume(ctx, queueName, c.opts.Prefetch, c.handleDelivery); err != nil {
			return err
		}
	}
	return nil
}

func (c *DeleteConsumer) Start(ctx context.Context, queueName string) error {
	for i := 0; i < c.opts.DeleteWorkers; i++ {
		if err := c.queue.Consume(ctx, queueName, c.opts.Prefetch, c.handleDelivery); err != nil {
			return err
		}
	}
	return nil
}

func (c *IndexConsumer) handleDelivery(ctx context.Context, d queue.Delivery) {
	var msg DocumentIndexMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		logger.Logger.Error("解析索引任务消息失败", zap.Error(err))
		_ = d.Ack()
		return
	}
	taskID, attempt, err := c.handle(ctx, msg)
	if err != nil {
		if stderrors.Is(err, errIndexLeaseActive) || stderrors.Is(err, repository.ErrDocumentIndexLeaseLost) {
			msg.Attempt = attempt
			c.waitIndexLease(ctx, d, msg)
			return
		}
		if stderrors.Is(err, errRedriveIndexDLQ) {
			msg.Attempt = attempt
			c.publishFinalIndexFailure(ctx, d, msg, err)
			return
		}
		c.retryIndex(ctx, d, msg, taskID, attempt, err)
		return
	}
	_ = d.Ack()
}

func (c *DeleteConsumer) handleDelivery(ctx context.Context, d queue.Delivery) {
	var msg DocumentDeleteCleanupMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		logger.Logger.Error("解析删除清理任务消息失败", zap.Error(err))
		_ = d.Ack()
		return
	}
	if err := c.handle(ctx, msg); err != nil {
		c.retryDelete(ctx, d, msg, err)
		return
	}
	_ = d.Ack()
}

func (c *IndexConsumer) handle(ctx context.Context, msg DocumentIndexMessage) (string, int, error) {
	doc, err := c.docs.FindByIDAndUserID(msg.DocumentID, msg.UserID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			if msg.ExternalDocumentID != 0 {
				_ = c.sources.MarkExternalDocumentFailed(msg.ExternalDocumentID, msg.DocumentID, "本地文档不存在")
			}
			return "", msg.Attempt, nil
		}
		return "", msg.Attempt, err
	}
	attempt := msg.Attempt
	if doc.IndexAttempt > attempt {
		attempt = doc.IndexAttempt
	}
	if doc.KnowledgeBaseID != msg.KnowledgeBaseID {
		return "", attempt, nil
	}
	if doc.Status == model.DocumentStatusCompleted {
		if msg.ExternalDocumentID != 0 {
			return "", attempt, c.sources.MarkExternalDocumentSynced(msg.ExternalDocumentID, msg.DocumentID)
		}
		return "", attempt, nil
	}
	if doc.Status == model.DocumentStatusFailed {
		if attempt > c.opts.MaxRetries {
			return doc.IndexTaskID, attempt, errRedriveIndexDLQ
		}
		message := doc.ErrorMessage
		if message == "" {
			message = "文档索引失败"
		}
		return doc.IndexTaskID, attempt, stderrors.New(message)
	}
	if doc.Status != model.DocumentStatusPending && doc.Status != model.DocumentStatusProcessing {
		return "", attempt, nil
	}
	taskID := uuid.NewString()
	now := time.Now()
	claimed, err := c.docs.ClaimDocumentIndex(doc.ID, doc.UserID, taskID, attempt, now, now.Add(c.opts.IndexLease))
	if err != nil {
		return "", attempt, err
	}
	if !claimed {
		current, findErr := c.docs.FindByIDAndUserID(msg.DocumentID, msg.UserID)
		if findErr != nil {
			if stderrors.Is(findErr, gorm.ErrRecordNotFound) {
				return "", attempt, nil
			}
			return "", attempt, findErr
		}
		if current.Status == model.DocumentStatusCompleted {
			if msg.ExternalDocumentID != 0 {
				return "", attempt, c.sources.MarkExternalDocumentSynced(msg.ExternalDocumentID, msg.DocumentID)
			}
			return "", attempt, nil
		}
		if current.IndexAttempt > attempt {
			attempt = current.IndexAttempt
		}
		if current.Status == model.DocumentStatusProcessing {
			return "", attempt, errIndexLeaseActive
		}
		return "", attempt, nil
	}
	if err := c.indexer.Index(ctx, msg.DocumentID, taskID); err != nil {
		return taskID, attempt, err
	}
	if msg.ExternalDocumentID != 0 {
		return taskID, attempt, c.sources.MarkExternalDocumentSynced(msg.ExternalDocumentID, msg.DocumentID)
	}
	return taskID, attempt, nil
}

func (c *DeleteConsumer) handle(ctx context.Context, msg DocumentDeleteCleanupMessage) error {
	mode := msg.Mode
	if mode == "" {
		mode = DeleteModeDelete
	}
	doc, err := c.docs.FindByIDAndUserID(msg.DocumentID, msg.UserID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			if mode == DeleteModeDelete && msg.ExternalDocumentID != 0 {
				return c.sources.MarkExternalDocumentDeleted(msg.ExternalDocumentID, msg.DocumentID)
			}
			return nil
		}
		return err
	}
	if mode == DeleteModeDeindex && doc.Status != model.DocumentStatusInactive {
		return nil
	}
	if mode == DeleteModeDelete && doc.Status != model.DocumentStatusDeleting {
		return nil
	}
	if mode == DeleteModeDeindex {
		claimed, err := c.docs.MarkDeindexing(doc.ID, doc.UserID)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
	}
	if err := c.qdrant.DeleteByDocument(ctx, doc.UserID, doc.KnowledgeBaseID, doc.ID); err != nil {
		return err
	}
	if c.keyword != nil {
		if err := c.keyword.DeleteByDocumentID(ctx, doc.UserID, doc.ID); err != nil {
			return err
		}
	}
	if err := c.chunks.DeleteByDocumentID(doc.UserID, doc.ID); err != nil {
		return err
	}
	if mode == DeleteModeDeindex {
		if _, err := c.docs.UpdateStatusIfCurrent(doc.ID, doc.UserID, model.DocumentStatusDeindexing, model.DocumentStatusInactive, "", 0); err != nil {
			return err
		}
		if msg.ExternalDocumentID != 0 {
			return c.sources.MarkExternalDocumentDeindexed(msg.ExternalDocumentID, msg.DocumentID)
		}
		return nil
	}
	objectKey := doc.ObjectKey
	if objectKey == "" {
		objectKey = msg.ObjectKey
	}
	if err := c.minio.DeleteObject(ctx, objectKey); err != nil {
		return err
	}
	if err := c.docs.DeleteByIDAndUserID(doc.ID, doc.UserID); err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			if msg.ExternalDocumentID != 0 {
				return c.sources.MarkExternalDocumentDeleted(msg.ExternalDocumentID, msg.DocumentID)
			}
			return nil
		}
		return err
	}
	if msg.ExternalDocumentID != 0 {
		return c.sources.MarkExternalDocumentDeleted(msg.ExternalDocumentID, msg.DocumentID)
	}
	return nil
}

func (c *IndexConsumer) retryIndex(ctx context.Context, d queue.Delivery, msg DocumentIndexMessage, taskID string, attempt int, err error) {
	next := attempt + 1
	msg.Attempt = next
	var stateErr error
	if next <= c.opts.MaxRetries {
		_, stateErr = c.docs.RequeueDocumentIndex(msg.DocumentID, msg.UserID, taskID, next, "索引失败，等待重试: "+err.Error())
	} else {
		_, stateErr = c.docs.FinalizeDocumentIndexFailure(msg.DocumentID, msg.UserID, taskID, next, err.Error())
	}
	if stateErr != nil {
		logger.Logger.Error("更新文档索引重试状态失败", zap.Error(stateErr))
		_ = d.Nack(true)
		return
	}
	if msg.ExternalDocumentID != 0 {
		_ = c.sources.MarkExternalDocumentFailed(msg.ExternalDocumentID, msg.DocumentID, err.Error())
	}
	c.retry(ctx, d, queue.DocumentIndexRetryRoutingKey, queue.DocumentIndexDLQRoutingKey, msg, next, err, "索引任务执行失败")
}

func (c *IndexConsumer) waitIndexLease(ctx context.Context, d queue.Delivery, msg DocumentIndexMessage) {
	if err := c.queue.PublishJSON(ctx, queue.DocumentIndexRetryRoutingKey, msg); err != nil {
		logger.Logger.Error("投递索引租约等待任务失败", zap.Error(err))
		_ = d.Nack(true)
		return
	}
	_ = d.Ack()
}

func (c *IndexConsumer) publishFinalIndexFailure(ctx context.Context, d queue.Delivery, msg DocumentIndexMessage, cause error) {
	if err := c.queue.PublishJSON(ctx, queue.DocumentIndexDLQRoutingKey, msg); err != nil {
		logger.Logger.Error("投递文档索引死信任务失败", zap.Error(err))
		_ = d.Nack(true)
		return
	}
	logger.Logger.Error("文档索引任务已进入失败终态", zap.Error(cause), zap.Int("attempt", msg.Attempt))
	_ = d.Ack()
}

func (c *DeleteConsumer) retryDelete(ctx context.Context, d queue.Delivery, msg DocumentDeleteCleanupMessage, err error) {
	next := msg.Attempt + 1
	msg.Attempt = next
	if msg.ExternalDocumentID != 0 {
		_ = c.sources.MarkExternalDocumentFailed(msg.ExternalDocumentID, msg.DocumentID, err.Error())
	}
	if msg.Mode == DeleteModeDeindex {
		_, _ = c.docs.UpdateStatusIfCurrent(msg.DocumentID, msg.UserID, model.DocumentStatusDeindexing, model.DocumentStatusInactive, err.Error(), 0)
	}
	c.retry(ctx, d, queue.DocumentDeleteRetryRoutingKey, queue.DocumentDeleteDLQRoutingKey, msg, next, err, "删除清理任务执行失败")
}

func (c *IndexConsumer) retry(ctx context.Context, d queue.Delivery, retryKey, dlqKey string, msg any, attempt int, err error, logMessage string) {
	publishRetry(ctx, c.queue, d, c.opts.MaxRetries, retryKey, dlqKey, msg, attempt, err, logMessage)
}

func (c *DeleteConsumer) retry(ctx context.Context, d queue.Delivery, retryKey, dlqKey string, msg any, attempt int, err error, logMessage string) {
	publishRetry(ctx, c.queue, d, c.opts.MaxRetries, retryKey, dlqKey, msg, attempt, err, logMessage)
}

func publishRetry(ctx context.Context, q taskConsumerQueue, d queue.Delivery, maxRetries int, retryKey, dlqKey string, msg any, attempt int, err error, logMessage string) {
	targetKey := retryKey
	if attempt > maxRetries {
		targetKey = dlqKey
	}
	if publishErr := q.PublishJSON(ctx, targetKey, msg); publishErr != nil {
		logger.Logger.Error("投递重试任务失败", zap.Error(publishErr), zap.String("routing_key", targetKey))
		_ = d.Nack(true)
		return
	}
	if attempt > maxRetries {
		logger.Logger.Error(logMessage, zap.Error(fmt.Errorf("超过最大重试次数: %w", err)), zap.Int("attempt", attempt))
	} else {
		logger.Logger.Warn(logMessage, zap.Error(err), zap.Int("attempt", attempt))
	}
	_ = d.Ack()
}

func optionsFromConfig(cfg config.RabbitMQConfig) ConsumerOptions {
	opts := ConsumerOptions{
		MaxRetries:       cfg.MaxRetries,
		Prefetch:         cfg.PrefetchCount,
		IndexWorkers:     cfg.IndexWorkers,
		IndexLease:       time.Duration(cfg.IndexLeaseSeconds) * time.Second,
		DeleteWorkers:    cfg.DeleteWorkers,
		SourceSyncWorker: cfg.SourceSyncWorkers,
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 5
	}
	if opts.Prefetch <= 0 {
		opts.Prefetch = 1
	}
	if opts.IndexWorkers <= 0 {
		opts.IndexWorkers = 2
	}
	if opts.IndexLease <= 0 {
		opts.IndexLease = 30 * time.Minute
	}
	if opts.DeleteWorkers <= 0 {
		opts.DeleteWorkers = 2
	}
	if opts.SourceSyncWorker <= 0 {
		opts.SourceSyncWorker = 1
	}
	return opts
}
