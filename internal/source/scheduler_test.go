package source

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
)

type fakeSchedulerRepository struct {
	mu      sync.Mutex
	sources map[uint64]model.KnowledgeSource
}

func (f *fakeSchedulerRepository) ListDueSources(now time.Time, limit int) ([]model.KnowledgeSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]model.KnowledgeSource, 0, limit)
	for _, source := range f.sources {
		if source.Enabled && source.SyncStatus == model.KnowledgeSourceStatusIdle && source.NextSyncAt != nil && !source.NextSyncAt.After(now) {
			result = append(result, source)
		}
	}
	return result, nil
}

func (f *fakeSchedulerRepository) ClaimDueSource(id uint64, now time.Time, taskID string, leaseUntil time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	source := f.sources[id]
	if !source.Enabled || source.SyncStatus != model.KnowledgeSourceStatusIdle || source.NextSyncAt == nil || source.NextSyncAt.After(now) {
		return false, nil
	}
	source.SyncStatus = model.KnowledgeSourceStatusQueued
	source.SyncTaskID = taskID
	source.SyncLeaseUntil = &leaseUntil
	f.sources[id] = source
	return true, nil
}

func (f *fakeSchedulerRepository) RestoreScheduledSource(id uint64, taskID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	source := f.sources[id]
	if source.SyncStatus == model.KnowledgeSourceStatusQueued && source.SyncTaskID == taskID {
		source.SyncStatus = model.KnowledgeSourceStatusIdle
		source.SyncTaskID = ""
		source.LastError = message
		f.sources[id] = source
	}
	return nil
}

func (f *fakeSchedulerRepository) ListExpiredSources(now time.Time, limit int) ([]model.KnowledgeSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]model.KnowledgeSource, 0, limit)
	for _, source := range f.sources {
		if source.Enabled && (source.SyncStatus == model.KnowledgeSourceStatusQueued || source.SyncStatus == model.KnowledgeSourceStatusSyncing) && source.SyncLeaseUntil != nil && !source.SyncLeaseUntil.After(now) {
			result = append(result, source)
		}
	}
	return result, nil
}

func (f *fakeSchedulerRepository) RecoverExpiredSource(id uint64, oldTaskID, newTaskID string, now, leaseUntil time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	source := f.sources[id]
	if source.SyncTaskID != oldTaskID || source.SyncLeaseUntil == nil || source.SyncLeaseUntil.After(now) {
		return false, nil
	}
	source.SyncStatus = model.KnowledgeSourceStatusQueued
	source.SyncTaskID = newTaskID
	source.SyncLeaseUntil = &leaseUntil
	f.sources[id] = source
	return true, nil
}

func (f *fakeSchedulerRepository) MarkRecoveryPublishFailed(id uint64, taskID, message string, retryAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	source := f.sources[id]
	if source.SyncTaskID == taskID {
		source.SyncLeaseUntil = &retryAt
		source.LastError = message
		f.sources[id] = source
	}
	return nil
}

type fakeSchedulerPublisher struct {
	mu         sync.Mutex
	published  int
	publishErr error
	taskIDs    []string
}

func (f *fakeSchedulerPublisher) PublishSourceSync(_ context.Context, _, _, _ uint64, taskID string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published++
	f.taskIDs = append(f.taskIDs, taskID)
	return f.publishErr
}

func TestSchedulersOnlyClaimDueSourceOnce(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	repo := &fakeSchedulerRepository{sources: map[uint64]model.KnowledgeSource{
		1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Enabled: true, SyncStatus: model.KnowledgeSourceStatusIdle, NextSyncAt: &due},
	}}
	publisher := &fakeSchedulerPublisher{}
	one := NewScheduler(repo, publisher, config.SourceSyncConfig{})
	two := NewScheduler(repo, publisher, config.SourceSyncConfig{})
	one.now = func() time.Time { return now }
	two.now = func() time.Time { return now }

	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); one.runOnce(context.Background()) }()
	go func() { defer wait.Done(); two.runOnce(context.Background()) }()
	wait.Wait()

	if publisher.published != 1 {
		t.Fatalf("published %d tasks", publisher.published)
	}
}

func TestSchedulerRestoresDueSourceWhenPublishFails(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	repo := &fakeSchedulerRepository{sources: map[uint64]model.KnowledgeSource{
		1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Enabled: true, SyncStatus: model.KnowledgeSourceStatusIdle, NextSyncAt: &due},
	}}
	publisher := &fakeSchedulerPublisher{publishErr: errors.New("rabbit unavailable")}
	scheduler := NewScheduler(repo, publisher, config.SourceSyncConfig{})
	scheduler.now = func() time.Time { return now }
	scheduler.runOnce(context.Background())

	source := repo.sources[1]
	if source.SyncStatus != model.KnowledgeSourceStatusIdle || source.LastError == "" {
		t.Fatalf("unexpected restored state: status=%s error=%q", source.SyncStatus, source.LastError)
	}
}

func TestSchedulerRecoversExpiredTaskWithNewTaskID(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	repo := &fakeSchedulerRepository{sources: map[uint64]model.KnowledgeSource{
		1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Enabled: true, SyncStatus: model.KnowledgeSourceStatusSyncing, SyncTaskID: "old-task", SyncAttempt: 1, SyncLeaseUntil: &expired},
	}}
	publisher := &fakeSchedulerPublisher{}
	scheduler := NewScheduler(repo, publisher, config.SourceSyncConfig{})
	scheduler.now = func() time.Time { return now }
	scheduler.runOnce(context.Background())

	source := repo.sources[1]
	if source.SyncStatus != model.KnowledgeSourceStatusQueued || source.SyncTaskID == "old-task" {
		t.Fatalf("unexpected recovered state: status=%s task=%s", source.SyncStatus, source.SyncTaskID)
	}
	if publisher.published != 1 || publisher.taskIDs[0] != source.SyncTaskID {
		t.Fatalf("unexpected recovered publish: count=%d ids=%v", publisher.published, publisher.taskIDs)
	}
}

func TestSchedulerDoesNotRestartFailedSource(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	repo := &fakeSchedulerRepository{sources: map[uint64]model.KnowledgeSource{
		1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Enabled: true, SyncStatus: model.KnowledgeSourceStatusFailed, NextSyncAt: &due},
	}}
	publisher := &fakeSchedulerPublisher{}
	scheduler := NewScheduler(repo, publisher, config.SourceSyncConfig{})
	scheduler.now = func() time.Time { return now }
	scheduler.runOnce(context.Background())
	if publisher.published != 0 {
		t.Fatalf("failed source published %d tasks", publisher.published)
	}
}
