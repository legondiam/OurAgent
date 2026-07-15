package source

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/tasks"
)

type fakeSourceConsumerRepository struct {
	mu     sync.Mutex
	source model.KnowledgeSource
}

func (f *fakeSourceConsumerRepository) FindSourceByID(uint64) (*model.KnowledgeSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copy := f.source
	return &copy, nil
}

func (f *fakeSourceConsumerRepository) MarkSourceSyncing(_ uint64, taskID string, attempt int, leaseUntil time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.source.SyncStatus != model.KnowledgeSourceStatusQueued || f.source.SyncTaskID != taskID {
		return false, nil
	}
	f.source.SyncStatus = model.KnowledgeSourceStatusSyncing
	f.source.SyncAttempt = attempt
	f.source.SyncLeaseUntil = &leaseUntil
	return true, nil
}

func (f *fakeSourceConsumerRepository) CompleteSourceSync(_ uint64, taskID string, _ int, _ model.SourceSyncStats) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.source.SyncStatus != model.KnowledgeSourceStatusSyncing || f.source.SyncTaskID != taskID {
		return false, nil
	}
	f.source.SyncStatus = model.KnowledgeSourceStatusIdle
	return true, nil
}

func (f *fakeSourceConsumerRepository) RequeueSourceSync(_ uint64, taskID string, attempt int, message string, leaseUntil time.Time, _ model.SourceSyncStats) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.source.SyncStatus != model.KnowledgeSourceStatusSyncing || f.source.SyncTaskID != taskID {
		return false, nil
	}
	f.source.SyncStatus = model.KnowledgeSourceStatusQueued
	f.source.SyncAttempt = attempt
	f.source.SyncLeaseUntil = &leaseUntil
	f.source.LastError = message
	return true, nil
}

func (f *fakeSourceConsumerRepository) FailSourceSync(_ uint64, taskID string, attempt int, message string, _ model.SourceSyncStats) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.source.SyncTaskID != taskID || (f.source.SyncStatus != model.KnowledgeSourceStatusQueued && f.source.SyncStatus != model.KnowledgeSourceStatusSyncing) {
		return false, nil
	}
	f.source.SyncStatus = model.KnowledgeSourceStatusFailed
	f.source.SyncAttempt = attempt
	f.source.LastError = message
	return true, nil
}

type fakeSourceQueue struct {
	mu         sync.Mutex
	routingKey []string
	publishErr error
}

func (f *fakeSourceQueue) Consume(context.Context, string, int, func(context.Context, queue.Delivery)) error {
	return nil
}

func (f *fakeSourceQueue) PublishJSON(_ context.Context, routingKey string, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routingKey = append(f.routingKey, routingKey)
	return f.publishErr
}

type fakeSourceSyncService struct {
	calls   atomic.Int32
	err     error
	started chan struct{}
	release chan struct{}
}

func (f *fakeSourceSyncService) Sync(context.Context, *model.KnowledgeSource) (model.SourceSyncStats, error) {
	f.calls.Add(1)
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	return model.SourceSyncStats{ScanCount: 1}, f.err
}

func TestSourceConsumerOrdinaryFailurePublishesRetry(t *testing.T) {
	repo := newQueuedSourceRepository(0)
	q := &fakeSourceQueue{}
	service := &fakeSourceSyncService{err: errors.New("temporary failure")}
	consumer := NewConsumer(q, repo, service, config.RabbitMQConfig{MaxRetries: 2})
	acked, nacked := deliveryState()
	consumer.handleDelivery(context.Background(), newSourceDelivery(t, 0, acked, nacked))

	if repo.source.SyncStatus != model.KnowledgeSourceStatusQueued || repo.source.SyncAttempt != 1 {
		t.Fatalf("unexpected retry state: status=%s attempt=%d", repo.source.SyncStatus, repo.source.SyncAttempt)
	}
	if len(q.routingKey) != 1 || q.routingKey[0] != queue.SourceSyncRetryRoutingKey {
		t.Fatalf("unexpected routing keys: %v", q.routingKey)
	}
	if acked.Load() != 1 || nacked.Load() != 0 {
		t.Fatalf("unexpected delivery result: ack=%d nack=%d", acked.Load(), nacked.Load())
	}
}

func TestSourceConsumerFinalFailurePublishesDLQ(t *testing.T) {
	repo := newQueuedSourceRepository(2)
	q := &fakeSourceQueue{}
	service := &fakeSourceSyncService{err: errors.New("permanent failure")}
	consumer := NewConsumer(q, repo, service, config.RabbitMQConfig{MaxRetries: 2})
	acked, nacked := deliveryState()
	consumer.handleDelivery(context.Background(), newSourceDelivery(t, 2, acked, nacked))

	if repo.source.SyncStatus != model.KnowledgeSourceStatusFailed || repo.source.SyncAttempt != 3 {
		t.Fatalf("unexpected final state: status=%s attempt=%d", repo.source.SyncStatus, repo.source.SyncAttempt)
	}
	if len(q.routingKey) != 1 || q.routingKey[0] != queue.SourceSyncDLQRoutingKey {
		t.Fatalf("unexpected routing keys: %v", q.routingKey)
	}
}

func TestSourceConsumerRetryPublishFailureKeepsQueuedAndNacks(t *testing.T) {
	repo := newQueuedSourceRepository(0)
	q := &fakeSourceQueue{publishErr: errors.New("rabbit unavailable")}
	service := &fakeSourceSyncService{err: errors.New("temporary failure")}
	consumer := NewConsumer(q, repo, service, config.RabbitMQConfig{MaxRetries: 2})
	acked, nacked := deliveryState()
	consumer.handleDelivery(context.Background(), newSourceDelivery(t, 0, acked, nacked))

	if repo.source.SyncStatus != model.KnowledgeSourceStatusQueued || repo.source.SyncAttempt != 1 {
		t.Fatalf("unexpected retry state: status=%s attempt=%d", repo.source.SyncStatus, repo.source.SyncAttempt)
	}
	if acked.Load() != 0 || nacked.Load() != 1 {
		t.Fatalf("unexpected delivery result: ack=%d nack=%d", acked.Load(), nacked.Load())
	}
}

func TestSourceConsumerDuplicateMessageOnlyRunsOnce(t *testing.T) {
	repo := newQueuedSourceRepository(0)
	q := &fakeSourceQueue{}
	service := &fakeSourceSyncService{started: make(chan struct{}, 1), release: make(chan struct{})}
	consumer := NewConsumer(q, repo, service, config.RabbitMQConfig{MaxRetries: 2})
	done := make(chan struct{})
	go func() {
		_, _, _ = consumer.handle(context.Background(), sourceMessage(0))
		close(done)
	}()
	<-service.started
	_, _, err := consumer.handle(context.Background(), sourceMessage(0))
	if err != nil {
		t.Fatal(err)
	}
	close(service.release)
	<-done
	if service.calls.Load() != 1 {
		t.Fatalf("service executed %d times", service.calls.Load())
	}
}

func TestSourceConsumerRedrivesFailedTaskWithoutExecution(t *testing.T) {
	repo := newQueuedSourceRepository(3)
	repo.source.SyncStatus = model.KnowledgeSourceStatusFailed
	q := &fakeSourceQueue{}
	service := &fakeSourceSyncService{}
	consumer := NewConsumer(q, repo, service, config.RabbitMQConfig{MaxRetries: 2})
	acked, nacked := deliveryState()
	consumer.handleDelivery(context.Background(), newSourceDelivery(t, 2, acked, nacked))

	if service.calls.Load() != 0 {
		t.Fatalf("failed task executed %d times", service.calls.Load())
	}
	if len(q.routingKey) != 1 || q.routingKey[0] != queue.SourceSyncDLQRoutingKey {
		t.Fatalf("unexpected routing keys: %v", q.routingKey)
	}
}

func newQueuedSourceRepository(attempt int) *fakeSourceConsumerRepository {
	return &fakeSourceConsumerRepository{source: model.KnowledgeSource{
		ID:              1,
		UserID:          2,
		KnowledgeBaseID: 3,
		Enabled:         true,
		SyncStatus:      model.KnowledgeSourceStatusQueued,
		SyncTaskID:      "task-1",
		SyncAttempt:     attempt,
	}}
}

func sourceMessage(attempt int) tasks.SourceSyncMessage {
	return tasks.SourceSyncMessage{
		EventID:         "task-1",
		Type:            tasks.TypeSourceSync,
		SourceID:        1,
		UserID:          2,
		KnowledgeBaseID: 3,
		Attempt:         attempt,
	}
}

func newSourceDelivery(t *testing.T, attempt int, acked, nacked *atomic.Int32) queue.Delivery {
	body, err := json.Marshal(sourceMessage(attempt))
	if err != nil {
		t.Fatal(err)
	}
	return queue.NewDelivery(body, func() error {
		acked.Add(1)
		return nil
	}, func(bool) error {
		nacked.Add(1)
		return nil
	})
}

func deliveryState() (*atomic.Int32, *atomic.Int32) {
	return &atomic.Int32{}, &atomic.Int32{}
}
