package source

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/repository"
	"OurAgent/internal/tasks"
	"OurAgent/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Consumer struct {
	queue   *queue.Client
	sources *repository.SourceRepository
	service *Service
	opts    consumerOptions
}

type consumerOptions struct {
	maxRetries int
	prefetch   int
	workers    int
}

func NewConsumer(q *queue.Client, sources *repository.SourceRepository, service *Service, cfg config.RabbitMQConfig) *Consumer {
	return &Consumer{
		queue:   q,
		sources: sources,
		service: service,
		opts:    sourceConsumerOptions(cfg),
	}
}

func (c *Consumer) Start(ctx context.Context, queueName string) error {
	for i := 0; i < c.opts.workers; i++ {
		if err := c.queue.Consume(ctx, queueName, c.opts.prefetch, c.handleDelivery); err != nil {
			return err
		}
	}
	return nil
}

func (c *Consumer) handleDelivery(ctx context.Context, d queue.Delivery) {
	var msg tasks.SourceSyncMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		logger.Logger.Error("解析知识源同步任务消息失败", zap.Error(err))
		_ = d.Ack()
		return
	}
	if err := c.handle(ctx, msg); err != nil {
		c.retry(ctx, d, msg, err)
		return
	}
	_ = d.Ack()
}

func (c *Consumer) handle(ctx context.Context, msg tasks.SourceSyncMessage) error {
	source, err := c.sources.FindSourceByID(msg.SourceID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if source.UserID != msg.UserID || source.KnowledgeBaseID != msg.KnowledgeBaseID {
		return nil
	}
	if source.SyncStatus != model.KnowledgeSourceStatusQueued {
		return nil
	}
	ok, err := c.sources.MarkSourceSyncing(source.ID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	source.SyncStatus = model.KnowledgeSourceStatusSyncing
	if err := c.service.Sync(ctx, source); err != nil {
		_ = c.sources.FailSourceSync(source.ID, err.Error())
		return err
	}
	return nil
}

func (c *Consumer) retry(ctx context.Context, d queue.Delivery, msg tasks.SourceSyncMessage, err error) {
	msg.Attempt++
	targetKey := queue.SourceSyncRetryRoutingKey
	if msg.Attempt > c.opts.maxRetries {
		targetKey = queue.SourceSyncDLQRoutingKey
	}
	if publishErr := c.queue.PublishJSON(ctx, targetKey, msg); publishErr != nil {
		logger.Logger.Error("投递知识源同步重试任务失败", zap.Error(publishErr), zap.String("routing_key", targetKey))
		_ = d.Nack(true)
		return
	}
	if msg.Attempt > c.opts.maxRetries {
		logger.Logger.Error("知识源同步任务执行失败", zap.Error(fmt.Errorf("超过最大重试次数: %w", err)), zap.Int("attempt", msg.Attempt))
	} else {
		logger.Logger.Warn("知识源同步任务执行失败", zap.Error(err), zap.Int("attempt", msg.Attempt))
	}
	_ = d.Ack()
}

func sourceConsumerOptions(cfg config.RabbitMQConfig) consumerOptions {
	opts := consumerOptions{
		maxRetries: cfg.MaxRetries,
		prefetch:   cfg.PrefetchCount,
		workers:    cfg.SourceSyncWorkers,
	}
	if opts.maxRetries <= 0 {
		opts.maxRetries = 5
	}
	if opts.prefetch <= 0 {
		opts.prefetch = 1
	}
	if opts.workers <= 0 {
		opts.workers = 1
	}
	return opts
}
