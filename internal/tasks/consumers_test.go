package tasks

import (
	"context"
	"testing"

	"OurAgent/internal/config"
	"OurAgent/internal/model"

	"gorm.io/gorm"
)

type fakeTaskDocuments struct {
	doc     *model.Document
	deleted bool
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

type fakeTaskIndexer struct{ calls int }

func (f *fakeTaskIndexer) Index(context.Context, uint64) error {
	f.calls++
	return nil
}

func TestIndexConsumerRepairsExternalStateForCompletedDocument(t *testing.T) {
	docs := &fakeTaskDocuments{doc: &model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusCompleted}}
	sources := &fakeTaskSources{}
	indexer := &fakeTaskIndexer{}
	consumer := NewIndexConsumer(nil, docs, sources, indexer, configForTaskTest())
	if err := consumer.handle(context.Background(), DocumentIndexMessage{
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
