package source

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/tasks"
	"OurAgent/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Consumer struct {
	queue   sourceConsumerQueue
	sources sourceConsumerRepository
	service sourceSyncService
	opts    consumerOptions
}

type sourceSyncService interface {
	Sync(ctx context.Context, source *model.KnowledgeSource) (model.SourceSyncStats, error)
}

type sourceConsumerQueue interface {
	Consume(ctx context.Context, queueName string, prefetch int, handler func(context.Context, queue.Delivery)) error
	PublishJSON(ctx context.Context, routingKey string, value any) error
}

type sourceConsumerRepository interface {
	FindSourceByID(id uint64) (*model.KnowledgeSource, error)
	MarkSourceSyncing(id uint64, taskID string, attempt int, leaseUntil time.Time) (bool, error)
	CompleteSourceSync(id uint64, taskID string, intervalSeconds int, stats model.SourceSyncStats) (bool, error)
	RequeueSourceSync(id uint64, taskID string, attempt int, message string, leaseUntil time.Time, stats model.SourceSyncStats) (bool, error)
	FailSourceSync(id uint64, taskID string, attempt int, message string, stats model.SourceSyncStats) (bool, error)
}

type consumerOptions struct {
	maxRetries int
	prefetch   int
	workers    int
	lease      time.Duration
}

var errRedriveSourceDLQ = stderrors.New("redrive source sync dlq")

func NewConsumer(q sourceConsumerQueue, sources sourceConsumerRepository, service sourceSyncService, cfg config.RabbitMQConfig, sourceCfg ...config.SourceSyncConfig) *Consumer {
	opts := sourceConsumerOptions(cfg)
	if len(sourceCfg) > 0 && sourceCfg[0].LeaseSeconds > 0 {
		opts.lease = time.Duration(sourceCfg[0].LeaseSeconds) * time.Second
	}
	return &Consumer{
		queue:   q,
		sources: sources,
		service: service,
		opts:    opts,
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
	stats, attempt, err := c.handle(ctx, msg)
	if err != nil {
		if stderrors.Is(err, errRedriveSourceDLQ) {
			msg.Attempt = attempt
			c.publishFinalFailure(ctx, d, msg, err)
			return
		}
		c.retry(ctx, d, msg, attempt, stats, err)
		return
	}
	_ = d.Ack()
}

func (c *Consumer) handle(ctx context.Context, msg tasks.SourceSyncMessage) (model.SourceSyncStats, int, error) {
	stats := model.SourceSyncStats{}
	source, err := c.sources.FindSourceByID(msg.SourceID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return stats, msg.Attempt, nil
		}
		return stats, msg.Attempt, err
	}
	if source.UserID != msg.UserID || source.KnowledgeBaseID != msg.KnowledgeBaseID {
		return stats, msg.Attempt, nil
	}
	if source.SyncTaskID != msg.EventID {
		return stats, msg.Attempt, nil
	}
	if source.SyncStatus == model.KnowledgeSourceStatusFailed && source.SyncAttempt > c.opts.maxRetries {
		return stats, source.SyncAttempt, errRedriveSourceDLQ
	}
	if source.SyncStatus != model.KnowledgeSourceStatusQueued {
		return stats, msg.Attempt, nil
	}
	attempt := msg.Attempt
	if source.SyncAttempt > attempt {
		attempt = source.SyncAttempt
	}
	leaseUntil := time.Now().Add(c.opts.lease)
	ok, err := c.sources.MarkSourceSyncing(source.ID, msg.EventID, attempt, leaseUntil)
	if err != nil {
		return stats, attempt, err
	}
	if !ok {
		return stats, attempt, nil
	}
	source.SyncStatus = model.KnowledgeSourceStatusSyncing
	source.SyncAttempt = attempt
	start := time.Now()
	stats, err = c.service.Sync(ctx, source)
	stats.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		return stats, attempt, err
	}
	completed, err := c.sources.CompleteSourceSync(source.ID, msg.EventID, source.SyncIntervalSeconds, stats)
	if err != nil {
		return stats, attempt, err
	}
	if !completed {
		return stats, attempt, nil
	}
	return stats, attempt, nil
}

func (c *Consumer) retry(ctx context.Context, d queue.Delivery, msg tasks.SourceSyncMessage, attempt int, stats model.SourceSyncStats, err error) {
	msg.Attempt = attempt + 1
	targetKey := queue.SourceSyncRetryRoutingKey
	if msg.Attempt > c.opts.maxRetries {
		targetKey = queue.SourceSyncDLQRoutingKey
	}
	var transitioned bool
	var stateErr error
	if msg.Attempt > c.opts.maxRetries {
		transitioned, stateErr = c.sources.FailSourceSync(msg.SourceID, msg.EventID, msg.Attempt, err.Error(), stats)
	} else {
		leaseUntil := time.Now().Add(c.opts.lease)
		transitioned, stateErr = c.sources.RequeueSourceSync(msg.SourceID, msg.EventID, msg.Attempt, err.Error(), leaseUntil, stats)
	}
	if stateErr != nil {
		logger.Logger.Error("更新知识源同步重试状态失败", zap.Error(stateErr))
		_ = d.Nack(true)
		return
	}
	if !transitioned {
		_ = d.Ack()
		return
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

func (c *Consumer) publishFinalFailure(ctx context.Context, d queue.Delivery, msg tasks.SourceSyncMessage, cause error) {
	if err := c.queue.PublishJSON(ctx, queue.SourceSyncDLQRoutingKey, msg); err != nil {
		logger.Logger.Error("投递知识源同步死信任务失败", zap.Error(err))
		_ = d.Nack(true)
		return
	}
	logger.Logger.Error("知识源同步任务已进入失败终态", zap.Error(cause), zap.Int("attempt", msg.Attempt))
	_ = d.Ack()
}

func sourceConsumerOptions(cfg config.RabbitMQConfig) consumerOptions {
	opts := consumerOptions{
		maxRetries: cfg.MaxRetries,
		prefetch:   cfg.PrefetchCount,
		workers:    cfg.SourceSyncWorkers,
		lease:      30 * time.Minute,
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
