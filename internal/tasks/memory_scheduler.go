package tasks

import (
	"context"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/repository"
	"OurAgent/pkg/logger"

	"go.uber.org/zap"
)

type MemoryScheduler struct {
	repo      *repository.LongTermMemoryRepository
	publisher Publisher
	cfg       config.LongTermMemoryConfig
}

// NewMemoryScheduler 创建长期记忆任务调度器
func NewMemoryScheduler(repo *repository.LongTermMemoryRepository, publisher Publisher, cfg config.LongTermMemoryConfig) *MemoryScheduler {
	return &MemoryScheduler{repo: repo, publisher: publisher, cfg: cfg}
}

// Start 启动长期记忆Signal和数据库任务调度
func (s *MemoryScheduler) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		return
	}
	interval := s.cfg.SchedulerIntervalSeconds
	if interval <= 0 {
		interval = 30
	}
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		s.run(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.run(ctx)
			}
		}
	}()
}

// run 执行一次长期记忆调度扫描
func (s *MemoryScheduler) run(ctx context.Context) {
	limit := s.cfg.SchedulerBatchSize
	if limit <= 0 {
		limit = 100
	}
	if s.cfg.ConsolidationEnabled {
		if _, err := s.repo.ScheduleSignalBatches(time.Now(), limit, s.cfg.ConsolidationMaxSignals, s.cfg.ConsolidationMaxInputTokens); err != nil {
			logger.Logger.Error("调度长期记忆候选失败", zap.Error(err))
		}
	}
	if err := s.repo.ExpireDue(time.Now(), limit); err != nil {
		logger.Logger.Error("处理长期记忆过期失败", zap.Error(err))
	}
	jobs, err := s.repo.QueuedJobs(limit, time.Now())
	if err != nil {
		logger.Logger.Error("查询长期记忆任务失败", zap.Error(err))
		return
	}
	lease := s.cfg.TaskLeaseSeconds
	if lease <= 0 {
		lease = 180
	}
	for _, job := range jobs {
		routingKey := memoryRoutingKey(job.Type)
		if routingKey == "" {
			continue
		}
		message := MemoryJobMessage{JobID: job.ID, Type: job.Type, UserID: job.UserID, Attempt: job.Attempt, CreatedAt: time.Now()}
		if job.MemoryID != nil {
			message.MemoryID = *job.MemoryID
		}
		if err := s.publisher.PublishJSON(ctx, routingKey, message); err != nil {
			_ = s.repo.FailJob(job.ID, err.Error())
			continue
		}
		_ = s.repo.MarkPublished(job.ID, time.Now().Add(time.Duration(lease)*time.Second))
	}
}

func memoryRoutingKey(jobType string) string {
	switch jobType {
	case model.MemoryJobConsolidate:
		return queue.MemoryConsolidateRoutingKey
	case model.MemoryJobIndex:
		return queue.MemoryIndexRoutingKey
	case model.MemoryJobDeleteVector, model.MemoryJobExpire:
		return queue.MemoryDeleteRoutingKey
	default:
		return ""
	}
}
