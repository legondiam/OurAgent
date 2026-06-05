package tasks

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"

	"OurAgent/internal/config"
	appdocument "OurAgent/internal/document"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/repository"
	appsearch "OurAgent/internal/search"
	"OurAgent/internal/storage"
	"OurAgent/internal/vectorstore"
	"OurAgent/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type ConsumerOptions struct {
	MaxRetries       int
	Prefetch         int
	IndexWorkers     int
	DeleteWorkers    int
	SourceSyncWorker int
}

type IndexConsumer struct {
	queue   *queue.Client
	docs    *repository.DocumentRepository
	indexer *appdocument.Indexer
	opts    ConsumerOptions
}

type DeleteConsumer struct {
	queue   *queue.Client
	docs    *repository.DocumentRepository
	chunks  *repository.ChunkRepository
	qdrant  *vectorstore.QdrantClient
	keyword appsearch.KeywordStore
	minio   *storage.MinIOClient
	opts    ConsumerOptions
}

func NewIndexConsumer(q *queue.Client, docs *repository.DocumentRepository, indexer *appdocument.Indexer, cfg config.RabbitMQConfig) *IndexConsumer {
	return &IndexConsumer{
		queue:   q,
		docs:    docs,
		indexer: indexer,
		opts:    optionsFromConfig(cfg),
	}
}

func NewDeleteConsumer(q *queue.Client, docs *repository.DocumentRepository, chunks *repository.ChunkRepository, qdrant *vectorstore.QdrantClient, keyword appsearch.KeywordStore, minio *storage.MinIOClient, cfg config.RabbitMQConfig) *DeleteConsumer {
	return &DeleteConsumer{
		queue:   q,
		docs:    docs,
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
	if err := c.handle(ctx, msg); err != nil {
		c.retryIndex(ctx, d, msg, err)
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

func (c *IndexConsumer) handle(ctx context.Context, msg DocumentIndexMessage) error {
	doc, err := c.docs.FindByIDAndUserID(msg.DocumentID, msg.UserID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if doc.Status == model.DocumentStatusDeleting || doc.Status != model.DocumentStatusPending {
		return nil
	}
	return c.indexer.Index(ctx, msg.DocumentID)
}

func (c *DeleteConsumer) handle(ctx context.Context, msg DocumentDeleteCleanupMessage) error {
	doc, err := c.docs.FindByIDAndUserID(msg.DocumentID, msg.UserID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if doc.Status != model.DocumentStatusDeleting {
		return nil
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
	objectKey := doc.ObjectKey
	if objectKey == "" {
		objectKey = msg.ObjectKey
	}
	if err := c.minio.DeleteObject(ctx, objectKey); err != nil {
		return err
	}
	if err := c.docs.DeleteByIDAndUserID(doc.ID, doc.UserID); err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func (c *IndexConsumer) retryIndex(ctx context.Context, d queue.Delivery, msg DocumentIndexMessage, err error) {
	next := msg.Attempt + 1
	msg.Attempt = next
	if next <= c.opts.MaxRetries {
		_ = c.docs.UpdateStatus(msg.DocumentID, msg.UserID, model.DocumentStatusPending, "索引失败，等待重试: "+err.Error(), 0)
	}
	c.retry(ctx, d, queue.DocumentIndexRetryRoutingKey, queue.DocumentIndexDLQRoutingKey, msg, next, err, "索引任务执行失败")
}

func (c *DeleteConsumer) retryDelete(ctx context.Context, d queue.Delivery, msg DocumentDeleteCleanupMessage, err error) {
	next := msg.Attempt + 1
	msg.Attempt = next
	c.retry(ctx, d, queue.DocumentDeleteRetryRoutingKey, queue.DocumentDeleteDLQRoutingKey, msg, next, err, "删除清理任务执行失败")
}

func (c *IndexConsumer) retry(ctx context.Context, d queue.Delivery, retryKey, dlqKey string, msg any, attempt int, err error, logMessage string) {
	publishRetry(ctx, c.queue, d, c.opts.MaxRetries, retryKey, dlqKey, msg, attempt, err, logMessage)
}

func (c *DeleteConsumer) retry(ctx context.Context, d queue.Delivery, retryKey, dlqKey string, msg any, attempt int, err error, logMessage string) {
	publishRetry(ctx, c.queue, d, c.opts.MaxRetries, retryKey, dlqKey, msg, attempt, err, logMessage)
}

func publishRetry(ctx context.Context, q *queue.Client, d queue.Delivery, maxRetries int, retryKey, dlqKey string, msg any, attempt int, err error, logMessage string) {
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
	if opts.DeleteWorkers <= 0 {
		opts.DeleteWorkers = 2
	}
	if opts.SourceSyncWorker <= 0 {
		opts.SourceSyncWorker = 1
	}
	return opts
}
