package source

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type fakeSourceServiceRepository struct {
	source   model.KnowledgeSource
	external []model.ExternalDocument
	nextID   uint64
}

func (f *fakeSourceServiceRepository) CreateSource(source *model.KnowledgeSource) error {
	f.source = *source
	return nil
}

func (f *fakeSourceServiceRepository) ListSources(uint64, uint64) ([]model.KnowledgeSource, error) {
	return []model.KnowledgeSource{f.source}, nil
}

func (f *fakeSourceServiceRepository) FindSourceByIDAndUserID(uint64, uint64) (*model.KnowledgeSource, error) {
	copy := f.source
	return &copy, nil
}

func (f *fakeSourceServiceRepository) MarkSourceQueued(uint64, uint64, string, time.Time) (bool, error) {
	return true, nil
}

func (f *fakeSourceServiceRepository) FailSourceSync(uint64, string, int, string, model.SourceSyncStats) (bool, error) {
	return true, nil
}

func (f *fakeSourceServiceRepository) ListExternalDocumentsForUser(uint64, uint64) ([]model.ExternalDocument, error) {
	return append([]model.ExternalDocument(nil), f.external...), nil
}

func (f *fakeSourceServiceRepository) ListExternalDocumentsBySource(uint64) ([]model.ExternalDocument, error) {
	return append([]model.ExternalDocument(nil), f.external...), nil
}

func (f *fakeSourceServiceRepository) CreateExternalDocument(doc *model.ExternalDocument) error {
	if f.nextID == 0 {
		f.nextID = 1
	}
	doc.ID = f.nextID
	f.nextID++
	f.external = append(f.external, *doc)
	return nil
}

func (f *fakeSourceServiceRepository) UpdateExternalDocument(doc *model.ExternalDocument) error {
	for i := range f.external {
		if f.external[i].ID == doc.ID {
			f.external[i] = *doc
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (f *fakeSourceServiceRepository) MarkExternalDocumentFailed(id, documentID uint64, message string) error {
	for i := range f.external {
		if f.external[i].ID == id && f.external[i].DocumentID == documentID {
			f.external[i].SyncStatus = model.ExternalDocumentStatusFailed
			f.external[i].LastError = message
		}
	}
	return nil
}

func (f *fakeSourceServiceRepository) MarkExternalDocumentMissing(id uint64, taskID string) (*model.ExternalDocument, error) {
	for i := range f.external {
		doc := &f.external[i]
		if doc.ID != id {
			continue
		}
		if doc.MissingTaskID != taskID {
			doc.MissingCount++
			doc.MissingTaskID = taskID
		}
		doc.SyncStatus = model.ExternalDocumentStatusMissing
		copy := *doc
		return &copy, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeSourceServiceRepository) MarkExternalDocumentDeleted(id, documentID uint64) error {
	for i := range f.external {
		if f.external[i].ID == id && f.external[i].DocumentID == documentID {
			f.external[i].SyncStatus = model.ExternalDocumentStatusDeleted
		}
	}
	return nil
}

type fakeSourceDocuments struct {
	docs   map[uint64]model.Document
	nextID uint64
}

func (f *fakeSourceDocuments) Create(doc *model.Document) error {
	if f.nextID == 0 {
		f.nextID = 1
	}
	doc.ID = f.nextID
	f.nextID++
	f.docs[doc.ID] = *doc
	return nil
}

func (f *fakeSourceDocuments) FindByIDAndUserID(id, userID uint64) (*model.Document, error) {
	doc, ok := f.docs[id]
	if !ok || doc.UserID != userID {
		return nil, gorm.ErrRecordNotFound
	}
	copy := doc
	return &copy, nil
}

func (f *fakeSourceDocuments) UpdateStatus(id, _ uint64, status, message string, chunkCount int) error {
	doc := f.docs[id]
	doc.Status = status
	doc.ErrorMessage = message
	doc.ChunkCount = chunkCount
	f.docs[id] = doc
	return nil
}

func (f *fakeSourceDocuments) UpdateSyncedDocument(doc *model.Document) error {
	f.docs[doc.ID] = *doc
	return nil
}

func (f *fakeSourceDocuments) UpdateSyncedDocumentIfStatus(doc *model.Document, currentStatus string) (bool, error) {
	current := f.docs[doc.ID]
	if current.Status != currentStatus {
		return false, nil
	}
	f.docs[doc.ID] = *doc
	return true, nil
}

type fakeSourceObjectStore struct{ uploads int }

func (f *fakeSourceObjectStore) Upload(context.Context, string, io.Reader, int64, string) error {
	f.uploads++
	return nil
}

func (f *fakeSourceObjectStore) Bucket() string { return "bucket" }

type fakeSourceTasks struct {
	indexes   int
	deindexes int
	deletes   int
}

func (f *fakeSourceTasks) PublishExternalDocumentIndex(context.Context, uint64, uint64, uint64, uint64) error {
	f.indexes++
	return nil
}

func (f *fakeSourceTasks) PublishExternalDocumentDeindex(context.Context, model.Document, uint64) error {
	f.deindexes++
	return nil
}

func (f *fakeSourceTasks) PublishExternalDocumentDelete(context.Context, model.Document, uint64) error {
	f.deletes++
	return nil
}

func (f *fakeSourceTasks) PublishSourceSync(context.Context, uint64, uint64, uint64, string, int) error {
	return nil
}

func TestSyncCreatesNewExternalDocument(t *testing.T) {
	repo := &fakeSourceServiceRepository{}
	docs := &fakeSourceDocuments{docs: map[uint64]model.Document{}}
	store := &fakeSourceObjectStore{}
	tasks := &fakeSourceTasks{}
	service := NewService(repo, nil, docs, store, tasks, config.SourceSyncConfig{DeleteAfterMissingSyncs: 2})
	source := manualTestSource(t, "task-1", manualTestDocument("doc-1", "new content"))

	stats, err := service.Sync(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CreatedCount != 1 || tasks.indexes != 1 || len(repo.external) != 1 {
		t.Fatalf("stats=%+v indexes=%d external=%d", stats, tasks.indexes, len(repo.external))
	}
	if repo.external[0].SyncStatus != model.ExternalDocumentStatusChanged {
		t.Fatalf("unexpected external status: %s", repo.external[0].SyncStatus)
	}
}

func TestSyncSkipsUnchangedDocument(t *testing.T) {
	updatedAt := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	content := "same content"
	repo := &fakeSourceServiceRepository{external: []model.ExternalDocument{{
		ID: 1, SourceID: 1, UserID: 2, KnowledgeBaseID: 3, DocumentID: 1, RemoteID: "doc-1",
		RemoteUpdatedAt: &updatedAt, ContentHash: HashContent(content), SyncStatus: model.ExternalDocumentStatusSynced,
	}}}
	docs := &fakeSourceDocuments{docs: map[uint64]model.Document{1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusCompleted}}}
	store := &fakeSourceObjectStore{}
	tasks := &fakeSourceTasks{}
	service := NewService(repo, nil, docs, store, tasks)
	source := manualTestSource(t, "task-1", manualTestDocument("doc-1", content))

	stats, err := service.Sync(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.UnchangedCount != 1 || store.uploads != 0 || tasks.indexes != 0 {
		t.Fatalf("stats=%+v uploads=%d indexes=%d", stats, store.uploads, tasks.indexes)
	}
}

func TestSyncUpdatesChangedDocument(t *testing.T) {
	repo := &fakeSourceServiceRepository{external: []model.ExternalDocument{{
		ID: 1, SourceID: 1, UserID: 2, KnowledgeBaseID: 3, DocumentID: 1, RemoteID: "doc-1",
		ContentHash: HashContent("old content"), SyncStatus: model.ExternalDocumentStatusSynced,
	}}}
	docs := &fakeSourceDocuments{docs: map[uint64]model.Document{1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusCompleted}}}
	tasks := &fakeSourceTasks{}
	service := NewService(repo, nil, docs, &fakeSourceObjectStore{}, tasks)
	source := manualTestSource(t, "task-1", manualTestDocument("doc-1", "new content"))

	stats, err := service.Sync(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.UpdatedCount != 1 || tasks.indexes != 1 || docs.docs[1].Status != model.DocumentStatusPending {
		t.Fatalf("stats=%+v indexes=%d status=%s", stats, tasks.indexes, docs.docs[1].Status)
	}
}

func TestSyncRepairsUnchangedContentWhenDocumentIsNotCompleted(t *testing.T) {
	content := "same content"
	repo := &fakeSourceServiceRepository{external: []model.ExternalDocument{{
		ID: 1, SourceID: 1, UserID: 2, KnowledgeBaseID: 3, DocumentID: 1, RemoteID: "doc-1",
		ContentHash: HashContent(content), SyncStatus: model.ExternalDocumentStatusSynced,
	}}}
	docs := &fakeSourceDocuments{docs: map[uint64]model.Document{1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusFailed}}}
	tasks := &fakeSourceTasks{}
	service := NewService(repo, nil, docs, &fakeSourceObjectStore{}, tasks)
	source := manualTestSource(t, "task-1", manualTestDocument("doc-1", content))

	stats, err := service.Sync(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.UpdatedCount != 1 || tasks.indexes != 1 || docs.docs[1].Status != model.DocumentStatusPending {
		t.Fatalf("stats=%+v indexes=%d status=%s", stats, tasks.indexes, docs.docs[1].Status)
	}
}

func TestSyncDeindexesThenDeletesConsecutiveMissingDocument(t *testing.T) {
	repo := &fakeSourceServiceRepository{external: []model.ExternalDocument{{
		ID: 1, SourceID: 1, UserID: 2, KnowledgeBaseID: 3, DocumentID: 1, RemoteID: "doc-1", SyncStatus: model.ExternalDocumentStatusSynced,
	}}}
	docs := &fakeSourceDocuments{docs: map[uint64]model.Document{1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusCompleted}}}
	tasks := &fakeSourceTasks{}
	service := NewService(repo, nil, docs, &fakeSourceObjectStore{}, tasks, config.SourceSyncConfig{DeleteAfterMissingSyncs: 2})

	first := manualTestSource(t, "task-1")
	stats, err := service.Sync(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if stats.MissingCount != 1 || tasks.deindexes != 1 || docs.docs[1].Status != model.DocumentStatusInactive {
		t.Fatalf("first stats=%+v deindexes=%d status=%s", stats, tasks.deindexes, docs.docs[1].Status)
	}

	second := manualTestSource(t, "task-2")
	stats, err = service.Sync(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if stats.DeletedCount != 1 || tasks.deletes != 1 || docs.docs[1].Status != model.DocumentStatusDeleting {
		t.Fatalf("second stats=%+v deletes=%d status=%s", stats, tasks.deletes, docs.docs[1].Status)
	}
}

func TestSyncRetryDoesNotIncrementMissingCountTwice(t *testing.T) {
	repo := &fakeSourceServiceRepository{external: []model.ExternalDocument{{
		ID: 1, SourceID: 1, UserID: 2, KnowledgeBaseID: 3, DocumentID: 1, RemoteID: "doc-1", SyncStatus: model.ExternalDocumentStatusSynced,
	}}}
	docs := &fakeSourceDocuments{docs: map[uint64]model.Document{1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusCompleted}}}
	tasks := &fakeSourceTasks{}
	service := NewService(repo, nil, docs, &fakeSourceObjectStore{}, tasks, config.SourceSyncConfig{DeleteAfterMissingSyncs: 2})
	source := manualTestSource(t, "same-task")
	if _, err := service.Sync(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Sync(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if repo.external[0].MissingCount != 1 || tasks.deletes != 0 {
		t.Fatalf("missing_count=%d deletes=%d", repo.external[0].MissingCount, tasks.deletes)
	}
}

func TestSyncReindexesMissingDocumentWhenItReappears(t *testing.T) {
	content := "restored content"
	repo := &fakeSourceServiceRepository{external: []model.ExternalDocument{{
		ID: 1, SourceID: 1, UserID: 2, KnowledgeBaseID: 3, DocumentID: 1, RemoteID: "doc-1",
		ContentHash: HashContent(content), SyncStatus: model.ExternalDocumentStatusMissing, MissingCount: 1, MissingTaskID: "old-task",
	}}}
	docs := &fakeSourceDocuments{docs: map[uint64]model.Document{1: {ID: 1, UserID: 2, KnowledgeBaseID: 3, Status: model.DocumentStatusInactive}}}
	tasks := &fakeSourceTasks{}
	service := NewService(repo, nil, docs, &fakeSourceObjectStore{}, tasks, config.SourceSyncConfig{DeleteAfterMissingSyncs: 2})
	source := manualTestSource(t, "new-task", manualTestDocument("doc-1", content))

	stats, err := service.Sync(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if stats.UpdatedCount != 1 || tasks.indexes != 1 || docs.docs[1].Status != model.DocumentStatusPending {
		t.Fatalf("stats=%+v indexes=%d status=%s", stats, tasks.indexes, docs.docs[1].Status)
	}
	if repo.external[0].MissingCount != 0 || repo.external[0].SyncStatus != model.ExternalDocumentStatusChanged {
		t.Fatalf("unexpected external state: %+v", repo.external[0])
	}
}

func manualTestSource(t *testing.T, taskID string, documents ...map[string]any) *model.KnowledgeSource {
	t.Helper()
	configJSON, err := json.Marshal(map[string]any{"documents": documents})
	if err != nil {
		t.Fatal(err)
	}
	return &model.KnowledgeSource{
		ID: 1, UserID: 2, KnowledgeBaseID: 3, Provider: ProviderManual,
		ConfigJSON: datatypes.JSON(configJSON), SyncTaskID: taskID, SyncIntervalSeconds: 60,
	}
}

func manualTestDocument(remoteID, markdown string) map[string]any {
	return map[string]any{
		"remote_id":  remoteID,
		"title":      remoteID,
		"updated_at": "2026-07-15T10:00:00Z",
		"markdown":   markdown,
	}
}
