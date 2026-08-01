package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/queue"

	"gorm.io/gorm"
)

type fakeTaskDocuments struct {
	doc          *model.Document
	deleted      bool
	claimCalls   int
	requeueCalls int
	finalCalls   int
}

func (f *fakeTaskDocuments) FindByIDAndUserID(uint64, uint64) (*model.Document, error) {
	if f.deleted || f.doc == nil {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *f.doc
	return &copy, nil
}

func (f *fakeTaskDocuments) DeleteByIDAndUserID(uint64, uint64) error {
	if f.deleted {
		return gorm.ErrRecordNotFound
	}
	f.deleted = true
	return nil
}

func (f *fakeTaskDocuments) UpdateStatus(_ uint64, _ uint64, status, message string, chunkCount int) error {
	f.doc.Status = status
	f.doc.ErrorMessage = message
	f.doc.ChunkCount = chunkCount
	return nil
}

func (f *fakeTaskDocuments) ClaimDocumentIndex(_ uint64, _ uint64, taskID string, attempt int, now, leaseUntil time.Time) (bool, error) {
	f.claimCalls++
	claimable := f.doc.Status == model.DocumentStatusPending || f.doc.Status == model.DocumentStatusProcessing && (f.doc.IndexLeaseUntil == nil || !f.doc.IndexLeaseUntil.After(now))
	if !claimable {
		return false, nil
	}
	f.doc.Status = model.DocumentStatusProcessing
	f.doc.IndexTaskID = taskID
	f.doc.IndexAttempt = attempt
	f.doc.IndexLeaseUntil = &leaseUntil
	return true, nil
}

func (f *fakeTaskDocuments) RequeueDocumentIndex(_ uint64, _ uint64, taskID string, attempt int, message string) (bool, error) {
	f.requeueCalls++
	if f.doc.IndexTaskID != taskID || f.doc.Status != model.DocumentStatusFailed && f.doc.Status != model.DocumentStatusProcessing {
		return false, nil
	}
	f.doc.Status = model.DocumentStatusPending
	f.doc.IndexAttempt = attempt
	f.doc.IndexLeaseUntil = nil
	f.doc.ErrorMessage = message
	return true, nil
}

func (f *fakeTaskDocuments) FinalizeDocumentIndexFailure(_ uint64, _ uint64, taskID string, attempt int, message string) (bool, error) {
	f.finalCalls++
	if f.doc.IndexTaskID != taskID || f.doc.Status != model.DocumentStatusFailed && f.doc.Status != model.DocumentStatusProcessing {
		return false, nil
	}
	f.doc.Status = model.DocumentStatusFailed
	f.doc.IndexAttempt = attempt
	f.doc.IndexLeaseUntil = nil
	f.doc.ErrorMessage = message
	return true, nil
}

func (f *fakeTaskDocuments) MarkDeindexing(uint64, uint64) (bool, error) {
	if f.doc.Status != model.DocumentStatusInactive {
		return false, nil
	}
	f.doc.Status = model.DocumentStatusDeindexing
	return true, nil
}

func (f *fakeTaskDocuments) UpdateStatusIfCurrent(_ uint64, _ uint64, currentStatus, status, message string, chunkCount int) (bool, error) {
	if f.doc.Status != currentStatus {
		return false, nil
	}
	f.doc.Status = status
	f.doc.ErrorMessage = message
	f.doc.ChunkCount = chunkCount
	return true, nil
}

type fakeTaskSources struct {
	syncedID    uint64
	deindexedID uint64
	failedID    uint64
	deletedID   uint64
}

func (f *fakeTaskSources) MarkExternalDocumentSynced(id, _ uint64) error {
	f.syncedID = id
	return nil
}

func (f *fakeTaskSources) MarkExternalDocumentDeindexed(id, _ uint64) error {
	f.deindexedID = id
	return nil
}

func (f *fakeTaskSources) MarkExternalDocumentFailed(id, _ uint64, _ string) error {
	f.failedID = id
	return nil
}

func (f *fakeTaskSources) MarkExternalDocumentDeleted(id, _ uint64) error {
	f.deletedID = id
	return nil
}

type fakeTaskChunks struct{ calls int }

func (f *fakeTaskChunks) DeleteByDocumentID(uint64, uint64) error {
	f.calls++
	return nil
}

type fakeTaskVectorStore struct{ calls int }

func (f *fakeTaskVectorStore) DeleteByDocument(context.Context, uint64, uint64, uint64) error {
	f.calls++
	return nil
}

type fakeTaskObjectStore struct{ calls int }

func (f *fakeTaskObjectStore) DeleteObject(context.Context, string) error {
	f.calls++
	return nil
}

type fakeTaskIndexer struct {
	calls  int
	taskID string
	err    error
}

func (f *fakeTaskIndexer) Index(_ context.Context, _ uint64, taskID string) error {
	f.calls++
	f.taskID = taskID
	return f.err
}

type fakeTaskQueue struct {
	routingKey string
	message    DocumentIndexMessage
	publishErr error
}

func (f *fakeTaskQueue) Consume(context.Context, string, int, func(context.Context, queue.Delivery)) error {
	return nil
}

func (f *fakeTaskQueue) PublishJSON(_ context.Context, routingKey string, value any) error {
	f.routingKey = routingKey
	if msg, ok := value.(DocumentIndexMessage); ok {
		f.message = msg
	}
	return f.publishErr
}

func TestIndexConsumerRepairsExternalStateForCompletedDocument(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusCompleted}}
	sources := &fakeTaskSources{}
	indexer := &fakeTaskIndexer{}
	consumer := NewIndexConsumer(nil, docs, sources, indexer, configForTaskTest())
	if _, _, err := consumer.handle(context.Background(), DocumentIndexMessage{
		DocumentID:         1,
		UserID:             2,
		KnowledgeBaseID:    3,
		ExternalDocumentID: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if indexer.calls != 0 || sources.syncedID != 4 {
		t.Fatalf("unexpected repair result: index_calls=%d external_id=%d", indexer.calls, sources.syncedID)
	}
}

func TestIndexConsumerClaimsPendingDocument(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusPending}}
	indexer := &fakeTaskIndexer{}
	consumer := NewIndexConsumer(nil, docs, &fakeTaskSources{}, indexer, configForTaskTest())
	taskID, attempt, err := consumer.handle(context.Background(), DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3})
	if err != nil {
		t.Fatal(err)
	}
	if taskID == "" || attempt != 0 || docs.claimCalls != 1 || indexer.calls != 1 || indexer.taskID != taskID {
		t.Fatalf("unexpected claim: task_id=%q attempt=%d claims=%d index_calls=%d", taskID, attempt, docs.claimCalls, indexer.calls)
	}
}

func TestIndexConsumerWaitsForActiveLease(t *testing.T) {
	leaseUntil := time.Now().Add(time.Minute)
	docs := &fakeTaskDocuments{doc: &model.Document{
		ID:              1,
		UserID:          2,
		KnowledgeBaseID: 3,
		Status:          model.DocumentStatusProcessing,
		IndexAttempt:    2,
		IndexLeaseUntil: &leaseUntil,
	}}
	indexer := &fakeTaskIndexer{}
	consumer := NewIndexConsumer(nil, docs, &fakeTaskSources{}, indexer, configForTaskTest())
	_, attempt, err := consumer.handle(context.Background(), DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3, Attempt: 1})
	if !errors.Is(err, errIndexLeaseActive) || attempt != 2 || indexer.calls != 0 {
		t.Fatalf("unexpected active lease result: attempt=%d index_calls=%d err=%v", attempt, indexer.calls, err)
	}
}

func TestIndexConsumerRecoversExpiredLease(t *testing.T) {
	leaseUntil := time.Now().Add(-time.Minute)
	docs := &fakeTaskDocuments{doc: &model.Document{
		ID:              1,
		UserID:          2,
		KnowledgeBaseID: 3,
		Status:          model.DocumentStatusProcessing,
		IndexTaskID:     "old-task",
		IndexLeaseUntil: &leaseUntil,
	}}
	indexer := &fakeTaskIndexer{}
	consumer := NewIndexConsumer(nil, docs, &fakeTaskSources{}, indexer, configForTaskTest())
	taskID, _, err := consumer.handle(context.Background(), DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3})
	if err != nil {
		t.Fatal(err)
	}
	if taskID == "" || taskID == "old-task" || docs.doc.IndexTaskID != taskID || indexer.calls != 1 {
		t.Fatalf("expired lease was not recovered: task_id=%q stored=%q calls=%d", taskID, docs.doc.IndexTaskID, indexer.calls)
	}
}

func TestIndexConsumerLeaseWaitDoesNotIncreaseAttempt(t *testing.T) {
	leaseUntil := time.Now().Add(time.Minute)
	docs := &fakeTaskDocuments{doc: &model.Document{
		ID:              1,
		UserID:          2,
		KnowledgeBaseID: 3,
		Status:          model.DocumentStatusProcessing,
		IndexAttempt:    2,
		IndexLeaseUntil: &leaseUntil,
	}}
	q := &fakeTaskQueue{}
	consumer := NewIndexConsumer(q, docs, &fakeTaskSources{}, &fakeTaskIndexer{}, configForTaskTest())
	body, err := json.Marshal(DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	acked := false
	nacked := false
	delivery := queue.NewDelivery(body, func() error {
		acked = true
		return nil
	}, func(bool) error {
		nacked = true
		return nil
	})
	consumer.handleDelivery(context.Background(), delivery)
	if !acked || nacked || q.routingKey != queue.DocumentIndexRetryRoutingKey || q.message.Attempt != 2 {
		t.Fatalf("unexpected lease wait: ack=%v nack=%v key=%s attempt=%d", acked, nacked, q.routingKey, q.message.Attempt)
	}
}

func TestIndexConsumerRetriesFailureWithPersistedAttempt(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusPending}}
	q := &fakeTaskQueue{}
	indexer := &fakeTaskIndexer{err: errors.New("embedding失败")}
	consumer := NewIndexConsumer(q, docs, &fakeTaskSources{}, indexer, configForTaskTest())
	body, err := json.Marshal(DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3})
	if err != nil {
		t.Fatal(err)
	}
	acked := false
	delivery := queue.NewDelivery(body, func() error {
		acked = true
		return nil
	}, func(bool) error { return nil })
	consumer.handleDelivery(context.Background(), delivery)
	if !acked || q.routingKey != queue.DocumentIndexRetryRoutingKey || q.message.Attempt != 1 || docs.doc.Status != model.DocumentStatusPending || docs.doc.IndexAttempt != 1 {
		t.Fatalf("unexpected retry: ack=%v key=%s message_attempt=%d status=%s stored_attempt=%d", acked, q.routingKey, q.message.Attempt, docs.doc.Status, docs.doc.IndexAttempt)
	}
}

func TestIndexConsumerResumesFailureAfterCrash(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{
		ID:              1,
		UserID:          2,
		KnowledgeBaseID: 3,
		Status:          model.DocumentStatusFailed,
		ErrorMessage:    "写入向量库失败",
		IndexTaskID:     "failed-task",
		IndexAttempt:    1,
	}}
	q := &fakeTaskQueue{}
	consumer := NewIndexConsumer(q, docs, &fakeTaskSources{}, &fakeTaskIndexer{}, configForTaskTest())
	body, err := json.Marshal(DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	acked := false
	delivery := queue.NewDelivery(body, func() error {
		acked = true
		return nil
	}, func(bool) error { return nil })
	consumer.handleDelivery(context.Background(), delivery)
	if !acked || q.routingKey != queue.DocumentIndexRetryRoutingKey || q.message.Attempt != 2 || docs.doc.Status != model.DocumentStatusPending || docs.requeueCalls != 1 {
		t.Fatalf("unexpected crash recovery: ack=%v key=%s attempt=%d status=%s requeues=%d", acked, q.routingKey, q.message.Attempt, docs.doc.Status, docs.requeueCalls)
	}
}

func TestIndexConsumerFailureEntersDLQ(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusPending, IndexAttempt: 2}}
	q := &fakeTaskQueue{}
	indexer := &fakeTaskIndexer{err: errors.New("索引失败")}
	consumer := NewIndexConsumer(q, docs, &fakeTaskSources{}, indexer, configForTaskTest())
	body, err := json.Marshal(DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3, Attempt: 2})
	if err != nil {
		t.Fatal(err)
	}
	acked := false
	delivery := queue.NewDelivery(body, func() error {
		acked = true
		return nil
	}, func(bool) error { return nil })
	consumer.handleDelivery(context.Background(), delivery)
	if !acked || q.routingKey != queue.DocumentIndexDLQRoutingKey || q.message.Attempt != 3 || docs.doc.Status != model.DocumentStatusFailed || docs.doc.IndexAttempt != 3 || docs.finalCalls != 1 {
		t.Fatalf("unexpected dlq: ack=%v key=%s attempt=%d status=%s stored_attempt=%d final_calls=%d", acked, q.routingKey, q.message.Attempt, docs.doc.Status, docs.doc.IndexAttempt, docs.finalCalls)
	}
}

func TestIndexConsumerLeaseWaitPublishFailureNacks(t *testing.T) {
	leaseUntil := time.Now().Add(time.Minute)
	docs := &fakeTaskDocuments{doc: &model.Document{
		ID:              1,
		UserID:          2,
		KnowledgeBaseID: 3,
		Status:          model.DocumentStatusProcessing,
		IndexLeaseUntil: &leaseUntil,
	}}
	q := &fakeTaskQueue{publishErr: errors.New("publish失败")}
	consumer := NewIndexConsumer(q, docs, &fakeTaskSources{}, &fakeTaskIndexer{}, configForTaskTest())
	body, err := json.Marshal(DocumentIndexMessage{DocumentID: 1, UserID: 2, KnowledgeBaseID: 3})
	if err != nil {
		t.Fatal(err)
	}
	acked := false
	nacked := false
	delivery := queue.NewDelivery(body, func() error {
		acked = true
		return nil
	}, func(requeue bool) error {
		nacked = requeue
		return nil
	})
	consumer.handleDelivery(context.Background(), delivery)
	if acked || !nacked {
		t.Fatalf("unexpected publish failure handling: ack=%v nack=%v", acked, nacked)
	}
}

func TestDeleteConsumerDeindexKeepsDocumentAndObject(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusInactive, ObjectKey: "doc.md"}}
	sources := &fakeTaskSources{}
	chunks := &fakeTaskChunks{}
	vectors := &fakeTaskVectorStore{}
	objects := &fakeTaskObjectStore{}
	consumer := NewDeleteConsumer(nil, docs, sources, chunks, vectors, nil, objects, configForTaskTest())
	err := consumer.handle(context.Background(), DocumentDeleteCleanupMessage{
		DocumentID:         1,
		UserID:             2,
		KnowledgeBaseID:    3,
		ExternalDocumentID: 4,
		Mode:               DeleteModeDeindex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if docs.deleted || objects.calls != 0 {
		t.Fatalf("deindex deleted persistent data: doc=%v object_calls=%d", docs.deleted, objects.calls)
	}
	if docs.doc.Status != model.DocumentStatusInactive || vectors.calls != 1 || chunks.calls != 1 || sources.deindexedID != 4 {
		t.Fatalf("unexpected deindex result: status=%s vectors=%d chunks=%d external_id=%d", docs.doc.Status, vectors.calls, chunks.calls, sources.deindexedID)
	}
}

func TestDeleteConsumerDeleteRemovesDocumentAndFinalizesMapping(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusDeleting, ObjectKey: "doc.md"}}
	sources := &fakeTaskSources{}
	chunks := &fakeTaskChunks{}
	vectors := &fakeTaskVectorStore{}
	objects := &fakeTaskObjectStore{}
	consumer := NewDeleteConsumer(nil, docs, sources, chunks, vectors, nil, objects, configForTaskTest())
	err := consumer.handle(context.Background(), DocumentDeleteCleanupMessage{
		DocumentID:         1,
		UserID:             2,
		KnowledgeBaseID:    3,
		ExternalDocumentID: 4,
		Mode:               DeleteModeDelete,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !docs.deleted || objects.calls != 1 || sources.deletedID != 4 {
		t.Fatalf("unexpected delete result: doc=%v object_calls=%d external_id=%d", docs.deleted, objects.calls, sources.deletedID)
	}
}

func TestDeleteConsumerFinalizesMissingDocument(t *testing.T) {
	docs := &fakeTaskDocuments{deleted: true}
	sources := &fakeTaskSources{}
	consumer := NewDeleteConsumer(nil, docs, sources, &fakeTaskChunks{}, &fakeTaskVectorStore{}, nil, &fakeTaskObjectStore{}, configForTaskTest())
	if err := consumer.handle(context.Background(), DocumentDeleteCleanupMessage{
		DocumentID:         1,
		UserID:             2,
		ExternalDocumentID: 4,
		Mode:               DeleteModeDelete,
	}); err != nil {
		t.Fatal(err)
	}
	if sources.deletedID != 4 {
		t.Fatalf("external document was not finalized: %d", sources.deletedID)
	}
}

func configForTaskTest() config.RabbitMQConfig {
	return config.RabbitMQConfig{MaxRetries: 2, PrefetchCount: 1}
}

func TestConsumerOptionsUsesConfiguredIndexLease(t *testing.T) {
	opts := optionsFromConfig(config.RabbitMQConfig{IndexLeaseSeconds: 45})
	if opts.IndexLease != 45*time.Second {
		t.Fatalf("unexpected index lease: %v", opts.IndexLease)
	}
}
