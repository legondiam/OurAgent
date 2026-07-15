package source

import (
	"context"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/pkg/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type SourceSyncSchedulerPublisher interface {
	PublishSourceSync(ctx context.Context, sourceID, userID, knowledgeBaseID uint64, taskID string, attempt int) error
}

type Scheduler struct {
	sources  sourceSchedulerRepository
	tasks    SourceSyncSchedulerPublisher
	interval time.Duration
	lease    time.Duration
	batch    int
	now      func() time.Time
}

type sourceSchedulerRepository interface {
	ListDueSources(now time.Time, limit int) ([]model.KnowledgeSource, error)
	ClaimDueSource(id uint64, now time.Time, taskID string, leaseUntil time.Time) (bool, error)
	RestoreScheduledSource(id uint64, taskID, message string) error
	ListExpiredSources(now time.Time, limit int) ([]model.KnowledgeSource, error)
	RecoverExpiredSource(id uint64, oldTaskID, newTaskID string, now, leaseUntil time.Time) (bool, error)
	MarkRecoveryPublishFailed(id uint64, taskID, message string, retryAt time.Time) error
}

func NewScheduler(sources sourceSchedulerRepository, tasks SourceSyncSchedulerPublisher, cfg config.SourceSyncConfig) *Scheduler {
	interval := time.Duration(cfg.SchedulerIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	lease := time.Duration(cfg.LeaseSeconds) * time.Second
	if lease <= 0 {
		lease = 30 * time.Minute
	}
	batch := cfg.ScheduleBatchSize
	if batch <= 0 {
		batch = 100
	}
	return &Scheduler{
		sources:  sources,
		tasks:    tasks,
		interval: interval,
		lease:    lease,
		batch:    batch,
		now:      time.Now,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go s.run(ctx)
}

func (s *Scheduler) run(ctx context.Context) {
	s.runOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context) {
	if err := s.recoverExpired(ctx); err != nil {
		logger.Logger.Error("恢复租约过期知识源失败", zap.Error(err))
	}
	if err := s.scheduleDue(ctx); err != nil {
		logger.Logger.Error("调度到期知识源失败", zap.Error(err))
	}
}

func (s *Scheduler) scheduleDue(ctx context.Context) error {
	now := s.now()
	sources, err := s.sources.ListDueSources(now, s.batch)
	if err != nil {
		return err
	}
	for i := range sources {
		source := sources[i]
		taskID := uuid.NewString()
		leaseUntil := now.Add(s.lease)
		claimed, err := s.sources.ClaimDueSource(source.ID, now, taskID, leaseUntil)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		if err := s.tasks.PublishSourceSync(ctx, source.ID, source.UserID, source.KnowledgeBaseID, taskID, 0); err != nil {
			_ = s.sources.RestoreScheduledSource(source.ID, taskID, err.Error())
			logger.Logger.Warn("投递定时知识源同步任务失败", zap.Uint64("source_id", source.ID), zap.Error(err))
		}
	}
	return nil
}

func (s *Scheduler) recoverExpired(ctx context.Context) error {
	now := s.now()
	sources, err := s.sources.ListExpiredSources(now, s.batch)
	if err != nil {
		return err
	}
	for i := range sources {
		source := sources[i]
		taskID := uuid.NewString()
		leaseUntil := now.Add(s.lease)
		claimed, err := s.sources.RecoverExpiredSource(source.ID, source.SyncTaskID, taskID, now, leaseUntil)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		if err := s.tasks.PublishSourceSync(ctx, source.ID, source.UserID, source.KnowledgeBaseID, taskID, source.SyncAttempt); err != nil {
			_ = s.sources.MarkRecoveryPublishFailed(source.ID, taskID, err.Error(), now)
			logger.Logger.Warn("投递知识源恢复任务失败", zap.Uint64("source_id", source.ID), zap.Error(err))
		}
	}
	return nil
}
