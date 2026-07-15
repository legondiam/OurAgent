package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"

	"github.com/google/uuid"
	pkgerrors "github.com/pkg/errors"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type SyncTaskPublisher interface {
	PublishExternalDocumentIndex(ctx context.Context, documentID, userID, knowledgeBaseID, externalDocumentID uint64) error
	PublishExternalDocumentDeindex(ctx context.Context, doc model.Document, externalDocumentID uint64) error
	PublishExternalDocumentDelete(ctx context.Context, doc model.Document, externalDocumentID uint64) error
	PublishSourceSync(ctx context.Context, sourceID, userID, knowledgeBaseID uint64, taskID string, attempt int) error
}

type Service struct {
	sources sourceServiceRepository
	kbs     sourceKnowledgeBaseRepository
	docs    sourceDocumentRepository
	minio   sourceObjectStore
	tasks   SyncTaskPublisher
	opts    config.SourceSyncConfig
}

type sourceServiceRepository interface {
	CreateSource(source *model.KnowledgeSource) error
	ListSources(userID, kbID uint64) ([]model.KnowledgeSource, error)
	FindSourceByIDAndUserID(id, userID uint64) (*model.KnowledgeSource, error)
	MarkSourceQueued(id, userID uint64, taskID string, leaseUntil time.Time) (bool, error)
	FailSourceSync(id uint64, taskID string, attempt int, message string, stats model.SourceSyncStats) (bool, error)
	ListExternalDocumentsForUser(sourceID, userID uint64) ([]model.ExternalDocument, error)
	ListExternalDocumentsBySource(sourceID uint64) ([]model.ExternalDocument, error)
	CreateExternalDocument(doc *model.ExternalDocument) error
	UpdateExternalDocument(doc *model.ExternalDocument) error
	MarkExternalDocumentFailed(id, documentID uint64, message string) error
	MarkExternalDocumentMissing(id uint64, taskID string) (*model.ExternalDocument, error)
	MarkExternalDocumentDeleted(id, documentID uint64) error
}

type sourceKnowledgeBaseRepository interface {
	ExistsByIDAndUserID(id, userID uint64) (bool, error)
}

type sourceDocumentRepository interface {
	Create(doc *model.Document) error
	FindByIDAndUserID(id, userID uint64) (*model.Document, error)
	UpdateStatus(id, userID uint64, status, message string, chunkCount int) error
	UpdateSyncedDocument(doc *model.Document) error
	UpdateSyncedDocumentIfStatus(doc *model.Document, currentStatus string) (bool, error)
}

type sourceObjectStore interface {
	Upload(ctx context.Context, objectKey string, reader io.Reader, size int64, contentType string) error
	Bucket() string
}

type CreateSourceInput struct {
	UserID              uint64
	KnowledgeBaseID     uint64
	Provider            string
	Name                string
	Config              json.RawMessage
	Credential          json.RawMessage
	SyncIntervalSeconds int
}

func NewService(sources sourceServiceRepository, kbs sourceKnowledgeBaseRepository, docs sourceDocumentRepository, minio sourceObjectStore, tasks SyncTaskPublisher, cfg ...config.SourceSyncConfig) *Service {
	opts := config.SourceSyncConfig{DeleteAfterMissingSyncs: 2}
	if len(cfg) > 0 {
		opts = cfg[0]
	}
	if opts.DeleteAfterMissingSyncs < 2 {
		opts.DeleteAfterMissingSyncs = 2
	}
	return &Service{sources: sources, kbs: kbs, docs: docs, minio: minio, tasks: tasks, opts: opts}
}

func (s *Service) CreateSource(input CreateSourceInput) (*model.KnowledgeSource, error) {
	exists, err := s.kbs.ExistsByIDAndUserID(input.KnowledgeBaseID, input.UserID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	if !exists {
		return nil, pkgerrors.WithStack(pkgerrors.New("知识库不存在"))
	}
	if strings.TrimSpace(input.Provider) == "" {
		return nil, pkgerrors.New("provider不能为空")
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, pkgerrors.New("name不能为空")
	}
	source := &model.KnowledgeSource{
		UserID:              input.UserID,
		KnowledgeBaseID:     input.KnowledgeBaseID,
		Provider:            strings.TrimSpace(input.Provider),
		Name:                strings.TrimSpace(input.Name),
		ConfigJSON:          datatypes.JSON(normalizeJSON(input.Config)),
		CredentialJSON:      datatypes.JSON(normalizeJSON(input.Credential)),
		Enabled:             true,
		SyncIntervalSeconds: input.SyncIntervalSeconds,
		SyncStatus:          model.KnowledgeSourceStatusIdle,
	}
	if err := s.sources.CreateSource(source); err != nil {
		return nil, err
	}
	return source, nil
}

func (s *Service) ListSources(userID, kbID uint64) ([]model.KnowledgeSource, error) {
	exists, err := s.kbs.ExistsByIDAndUserID(kbID, userID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	if !exists {
		return nil, pkgerrors.New("知识库不存在")
	}
	return s.sources.ListSources(userID, kbID)
}

func (s *Service) TriggerSync(ctx context.Context, userID, sourceID uint64) (*model.KnowledgeSource, error) {
	source, err := s.sources.FindSourceByIDAndUserID(sourceID, userID)
	if err != nil {
		return nil, err
	}
	taskID := uuid.NewString()
	leaseUntil := time.Now().Add(s.leaseDuration())
	queued, err := s.sources.MarkSourceQueued(source.ID, userID, taskID, leaseUntil)
	if err != nil {
		return nil, err
	}
	if queued {
		source.SyncStatus = model.KnowledgeSourceStatusQueued
		source.SyncTaskID = taskID
		source.SyncAttempt = 0
		source.SyncLeaseUntil = &leaseUntil
		source.LastError = ""
		if err := s.tasks.PublishSourceSync(ctx, source.ID, source.UserID, source.KnowledgeBaseID, taskID, 0); err != nil {
			_, _ = s.sources.FailSourceSync(source.ID, taskID, 0, err.Error(), model.SourceSyncStats{})
			return nil, pkgerrors.WithMessage(err, "投递知识源同步任务失败")
		}
	}
	return source, nil
}

func (s *Service) leaseDuration() time.Duration {
	seconds := s.opts.LeaseSeconds
	if seconds <= 0 {
		seconds = 1800
	}
	return time.Duration(seconds) * time.Second
}

func (s *Service) ListExternalDocuments(userID, sourceID uint64) ([]model.ExternalDocument, error) {
	if _, err := s.sources.FindSourceByIDAndUserID(sourceID, userID); err != nil {
		return nil, err
	}
	return s.sources.ListExternalDocumentsForUser(sourceID, userID)
}

func (s *Service) Sync(ctx context.Context, source *model.KnowledgeSource) (model.SourceSyncStats, error) {
	stats := model.SourceSyncStats{}
	connector, err := NewConnector(source.Provider)
	if err != nil {
		return stats, err
	}
	list, err := connector.List(ctx, ListRequest{
		Config:     source.ConfigJSON,
		Credential: source.CredentialJSON,
	})
	if err != nil {
		return stats, err
	}
	stats.ScanCount = len(list.Items)
	localDocs, err := s.sources.ListExternalDocumentsBySource(source.ID)
	if err != nil {
		return stats, err
	}
	localByRemoteID := make(map[string]model.ExternalDocument, len(localDocs))
	for _, doc := range localDocs {
		localByRemoteID[doc.RemoteID] = doc
	}
	seenRemoteIDs := make(map[string]struct{}, len(list.Items))
	for _, item := range list.Items {
		seenRemoteIDs[item.RemoteID] = struct{}{}
		local, exists := localByRemoteID[item.RemoteID]
		if exists && !shouldFetch(local, item) {
			doc, err := s.docs.FindByIDAndUserID(local.DocumentID, source.UserID)
			if err == nil && doc.Status == model.DocumentStatusCompleted {
				now := time.Now()
				local.LastSyncedAt = &now
				local.SyncStatus = model.ExternalDocumentStatusSynced
				local.LastError = ""
				if err := s.sources.UpdateExternalDocument(&local); err != nil {
					stats.FailedCount++
					return stats, err
				}
				stats.UnchangedCount++
				continue
			}
			if err != nil && !stderrors.Is(err, gorm.ErrRecordNotFound) {
				stats.FailedCount++
				return stats, err
			}
		}
		remote, err := connector.Fetch(ctx, FetchRequest{
			Config:     source.ConfigJSON,
			Credential: source.CredentialJSON,
			RemoteID:   item.RemoteID,
		})
		if err != nil {
			stats.FailedCount++
			if exists {
				local.SyncStatus = model.ExternalDocumentStatusFailed
				local.LastError = err.Error()
				_ = s.sources.UpdateExternalDocument(&local)
			}
			return stats, err
		}
		outcome, err := s.upsertRemoteDocument(ctx, source, remote, local, exists)
		if err != nil {
			stats.FailedCount++
			return stats, err
		}
		switch outcome {
		case syncOutcomeCreated:
			stats.CreatedCount++
		case syncOutcomeUpdated:
			stats.UpdatedCount++
		default:
			stats.UnchangedCount++
		}
	}
	for i := range localDocs {
		local := localDocs[i]
		if local.SyncStatus == model.ExternalDocumentStatusDeleted {
			continue
		}
		if _, seen := seenRemoteIDs[local.RemoteID]; seen {
			continue
		}
		missing, err := s.handleMissingDocument(ctx, source, &local)
		if err != nil {
			stats.FailedCount++
			return stats, err
		}
		if missing.MissingCount >= s.opts.DeleteAfterMissingSyncs {
			stats.DeletedCount++
		} else {
			stats.MissingCount++
		}
	}
	return stats, nil
}

const (
	syncOutcomeCreated   = "created"
	syncOutcomeUpdated   = "updated"
	syncOutcomeUnchanged = "unchanged"
)

func (s *Service) upsertRemoteDocument(ctx context.Context, source *model.KnowledgeSource, remote *RemoteDocument, local model.ExternalDocument, exists bool) (string, error) {
	if remote.ContentHash == "" {
		remote.ContentHash = HashContent(remote.Markdown)
	}
	var existingDoc *model.Document
	if exists && local.DocumentID != 0 {
		doc, err := s.docs.FindByIDAndUserID(local.DocumentID, source.UserID)
		if err == nil {
			existingDoc = doc
		} else if !stderrors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
	}
	if existingDoc != nil && (existingDoc.Status == model.DocumentStatusDeindexing || existingDoc.Status == model.DocumentStatusDeleting) {
		return "", pkgerrors.New("文档正在清理，等待重试")
	}
	if existingDoc != nil && existingDoc.Status == model.DocumentStatusProcessing {
		return "", pkgerrors.New("文档正在索引，等待重试")
	}
	if exists && local.ContentHash == remote.ContentHash && existingDoc != nil && existingDoc.Status == model.DocumentStatusCompleted {
		now := time.Now()
		local.RemoteUpdatedAt = &remote.UpdatedAt
		local.LastSyncedAt = &now
		local.SyncStatus = model.ExternalDocumentStatusSynced
		local.MissingCount = 0
		local.MissingTaskID = ""
		local.LastMissingAt = nil
		local.LastError = ""
		return syncOutcomeUnchanged, s.sources.UpdateExternalDocument(&local)
	}
	objectKey := buildObjectKey(source, remote)
	body := strings.NewReader(remote.Markdown)
	if err := s.minio.Upload(ctx, objectKey, body, int64(body.Len()), "text/markdown"); err != nil {
		return "", err
	}
	filename := safeMarkdownFilename(remote.Title, remote.RemoteID)
	doc := &model.Document{
		KnowledgeBaseID: source.KnowledgeBaseID,
		UserID:          source.UserID,
		Filename:        filename,
		FileType:        "md",
		FilePath:        objectKey,
		BucketName:      s.minio.Bucket(),
		ObjectKey:       objectKey,
		FileSize:        int64(len(remote.Markdown)),
		ContentType:     "text/markdown",
		Status:          model.DocumentStatusPending,
		ErrorMessage:    "",
		ChunkCount:      0,
	}
	created := existingDoc == nil
	if existingDoc != nil {
		doc.ID = existingDoc.ID
		if existingDoc.Status == model.DocumentStatusInactive {
			updated, err := s.docs.UpdateSyncedDocumentIfStatus(doc, model.DocumentStatusInactive)
			if err != nil {
				return "", err
			}
			if !updated {
				return "", pkgerrors.New("文档状态已变化，等待重试")
			}
		} else if err := s.docs.UpdateSyncedDocument(doc); err != nil {
			return "", err
		}
	} else {
		if err := s.docs.Create(doc); err != nil {
			return "", err
		}
	}
	now := time.Now()
	external := &model.ExternalDocument{
		SourceID:        source.ID,
		UserID:          source.UserID,
		KnowledgeBaseID: source.KnowledgeBaseID,
		DocumentID:      doc.ID,
		Provider:        source.Provider,
		RemoteID:        remote.RemoteID,
		RemoteURL:       remote.URL,
		RemoteTitle:     remote.Title,
		RemoteUpdatedAt: &remote.UpdatedAt,
		ContentHash:     remote.ContentHash,
		SyncStatus:      model.ExternalDocumentStatusChanged,
		MissingCount:    0,
		MissingTaskID:   "",
		LastSyncedAt:    &now,
		LastError:       "",
	}
	if exists {
		external.ID = local.ID
		external.CreatedAt = local.CreatedAt
		if err := s.sources.UpdateExternalDocument(external); err != nil {
			return "", err
		}
	} else if err := s.sources.CreateExternalDocument(external); err != nil {
		return "", err
	}
	if err := s.tasks.PublishExternalDocumentIndex(ctx, doc.ID, doc.UserID, doc.KnowledgeBaseID, external.ID); err != nil {
		_ = s.docs.UpdateStatus(doc.ID, doc.UserID, model.DocumentStatusFailed, "索引任务投递失败: "+err.Error(), 0)
		_ = s.sources.MarkExternalDocumentFailed(external.ID, doc.ID, err.Error())
		return "", err
	}
	if created {
		return syncOutcomeCreated, nil
	}
	return syncOutcomeUpdated, nil
}

func (s *Service) handleMissingDocument(ctx context.Context, source *model.KnowledgeSource, local *model.ExternalDocument) (*model.ExternalDocument, error) {
	missing, err := s.sources.MarkExternalDocumentMissing(local.ID, source.SyncTaskID)
	if err != nil {
		return nil, err
	}
	doc, err := s.docs.FindByIDAndUserID(missing.DocumentID, source.UserID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			if err := s.sources.MarkExternalDocumentDeleted(missing.ID, missing.DocumentID); err != nil {
				return nil, err
			}
			missing.SyncStatus = model.ExternalDocumentStatusDeleted
			return missing, nil
		}
		return nil, err
	}
	if missing.MissingCount >= s.opts.DeleteAfterMissingSyncs {
		if doc.Status != model.DocumentStatusDeleting {
			if err := s.docs.UpdateStatus(doc.ID, doc.UserID, model.DocumentStatusDeleting, "", doc.ChunkCount); err != nil {
				return nil, err
			}
			doc.Status = model.DocumentStatusDeleting
		}
		if err := s.tasks.PublishExternalDocumentDelete(ctx, *doc, missing.ID); err != nil {
			_ = s.sources.MarkExternalDocumentFailed(missing.ID, doc.ID, err.Error())
			return nil, err
		}
		return missing, nil
	}
	if doc.Status == model.DocumentStatusDeindexing {
		return missing, nil
	}
	if doc.Status != model.DocumentStatusInactive {
		if err := s.docs.UpdateStatus(doc.ID, doc.UserID, model.DocumentStatusInactive, "", doc.ChunkCount); err != nil {
			return nil, err
		}
		doc.Status = model.DocumentStatusInactive
	}
	if err := s.tasks.PublishExternalDocumentDeindex(ctx, *doc, missing.ID); err != nil {
		_ = s.sources.MarkExternalDocumentFailed(missing.ID, doc.ID, err.Error())
		return nil, err
	}
	return missing, nil
}

func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func shouldFetch(local model.ExternalDocument, item RemoteItem) bool {
	if item.AlwaysFetch {
		return true
	}
	if local.SyncStatus == model.ExternalDocumentStatusMissing ||
		local.SyncStatus == model.ExternalDocumentStatusDeleted ||
		local.SyncStatus == model.ExternalDocumentStatusChanged ||
		local.SyncStatus == model.ExternalDocumentStatusFailed {
		return true
	}
	if item.ContentHash != "" && item.ContentHash != local.ContentHash {
		return true
	}
	if local.RemoteUpdatedAt != nil && item.UpdatedAt.After(*local.RemoteUpdatedAt) {
		return true
	}
	return local.DocumentID == 0
}

func buildObjectKey(source *model.KnowledgeSource, remote *RemoteDocument) string {
	remoteID := sanitizePathPart(remote.RemoteID)
	return fmt.Sprintf("users/%d/knowledge-bases/%d/sources/%d/%s.md", source.UserID, source.KnowledgeBaseID, source.ID, remoteID)
}

func safeMarkdownFilename(title, fallback string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = fallback
	}
	name = filepath.Base(name)
	ext := strings.TrimSpace(filepath.Ext(name))
	if ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	return name + ".md"
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, "/", "_")
	if value == "" {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return value
}

func normalizeJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
