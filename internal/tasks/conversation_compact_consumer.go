package tasks

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/queue"
	"OurAgent/internal/repository"
	"OurAgent/pkg/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type conversationCompactor interface {
	Compact(ctx context.Context, task repository.ConversationCompactionTask) error
}

type ConversationCompactConsumer struct {
	queue         taskConsumerQueue
	conversations *repository.ConversationRepository
	compactor     conversationCompactor
	cfg           config.Config
}

func NewConversationCompactConsumer(q taskConsumerQueue, conversations *repository.ConversationRepository, compactor conversationCompactor, cfg *config.Config) *ConversationCompactConsumer {
	return &ConversationCompactConsumer{queue: q, conversations: conversations, compactor: compactor, cfg: *cfg}
}

func (c *ConversationCompactConsumer) Start(ctx context.Context, queueName string) error {
	workers := c.cfg.Rabbit.ConversationCompactWorkers
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		if err := c.queue.Consume(ctx, queueName, c.cfg.Rabbit.PrefetchCount, c.handleDelivery); err != nil {
			return err
		}
	}
	return nil
}

func (c *ConversationCompactConsumer) handleDelivery(ctx context.Context, delivery queue.Delivery) {
	var msg ConversationCompactMessage
	if err := json.Unmarshal(delivery.Body, &msg); err != nil {
		logger.Logger.Error("解析会话摘要任务失败", zap.Error(err))
		_ = delivery.Ack()
		return
	}
	task := repository.ConversationCompactionTask{
		TaskID:             msg.EventID,
		ConversationID:     msg.ConversationID,
		UserID:             msg.UserID,
		KnowledgeBaseID:    msg.KnowledgeBaseID,
		SnapshotLastLogID:  msg.SnapshotLastLogID,
		BaseSummaryVersion: msg.BaseSummaryVersion,
		Attempt:            msg.Attempt,
	}
	if err := c.compactor.Compact(ctx, task); err != nil {
		if stderrors.Is(err, repository.ErrConversationSummaryLeaseActive) {
			if publishErr := c.queue.PublishJSON(ctx, queue.ConversationCompactRetryRoutingKey, msg); publishErr != nil {
				_ = delivery.Nack(true)
				return
			}
			_ = delivery.Ack()
			return
		}
		c.retry(ctx, delivery, msg, err)
		return
	}
	_ = delivery.Ack()
	c.queueNext(ctx, msg)
}

func (c *ConversationCompactConsumer) retry(ctx context.Context, delivery queue.Delivery, msg ConversationCompactMessage, cause error) {
	next := msg.Attempt + 1
	msg.Attempt = next
	_ = c.conversations.FailCompaction(msg.ConversationID, msg.EventID, next, cause.Error())
	publishRetry(ctx, c.queue, delivery, c.cfg.Rabbit.MaxRetries, queue.ConversationCompactRetryRoutingKey, queue.ConversationCompactDLQRoutingKey, msg, next, cause, "会话摘要任务执行失败")
}

func (c *ConversationCompactConsumer) queueNext(ctx context.Context, completed ConversationCompactMessage) {
	taskID := uuid.NewString()
	now := time.Now()
	leaseSeconds := c.cfg.Memory.CompactionLeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = 180
	}
	task, err := c.conversations.TryQueueCompaction(completed.UserID, completed.KnowledgeBaseID, completed.ConversationID, taskID, c.cfg.Memory.SummaryTriggerTokens, now, now.Add(time.Duration(leaseSeconds)*time.Second))
	if err != nil || task == nil {
		return
	}
	msg := ConversationCompactMessage{
		EventID:            task.TaskID,
		Type:               TypeConversationCompact,
		ConversationID:     task.ConversationID,
		UserID:             task.UserID,
		KnowledgeBaseID:    task.KnowledgeBaseID,
		SnapshotLastLogID:  task.SnapshotLastLogID,
		BaseSummaryVersion: task.BaseSummaryVersion,
		Attempt:            task.Attempt,
		CreatedAt:          time.Now(),
	}
	if err := c.queue.PublishJSON(ctx, queue.ConversationCompactRoutingKey, msg); err != nil {
		_ = c.conversations.FailCompaction(task.ConversationID, task.TaskID, task.Attempt, err.Error())
		logger.Logger.Error("发布后续会话摘要任务失败", zap.Error(err))
	}
}
