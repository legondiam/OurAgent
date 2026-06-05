package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	"OurAgent/internal/storage"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/datatypes"
)

type SyncTaskPublisher interface {
	PublishDocumentIndex(ctx context.Context, documentID, userID, knowledgeBaseID uint64) error
	PublishSourceSync(ctx context.Context, sourceID, userID, knowledgeBaseID uint64) error
}

type Service struct {
	sources *repository.SourceRepository
	kbs     *repository.KnowledgeBaseRepository
	docs    *repository.DocumentRepository
	minio   *storage.MinIOClient
	tasks   SyncTaskPublisher
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

func NewService(sources *repository.SourceRepository, kbs *repository.KnowledgeBaseRepository, docs *repository.DocumentRepository, minio *storage.MinIOClient, tasks SyncTaskPublisher) *Service {
	return &Service{sources: sources, kbs: kbs, docs: docs, minio: minio, tasks: tasks}
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
	queued, err := s.sources.MarkSourceQueued(source.ID, userID)
	if err != nil {
		return nil, err
	}
	if queued {
		source.SyncStatus = model.KnowledgeSourceStatusQueued
		source.LastError = ""
		if err := s.tasks.PublishSourceSync(ctx, source.ID, source.UserID, source.KnowledgeBaseID); err != nil {
			_ = s.sources.FailSourceSync(source.ID, err.Error())
			return nil, pkgerrors.WithMessage(err, "投递知识源同步任务失败")
		}
	}
	return source, nil
}

func (s *Service) ListExternalDocuments(userID, sourceID uint64) ([]model.ExternalDocument, error) {
	if _, err := s.sources.FindSourceByIDAndUserID(sourceID, userID); err != nil {
		return nil, err
	}
	return s.sources.ListExternalDocumentsForUser(sourceID, userID)
}

func (s *Service) Sync(ctx context.Context, source *model.KnowledgeSource) error {
	connector, err := NewConnector(source.Provider)
	if err != nil {
		return err
	}
	list, err := connector.List(ctx, ListRequest{
		Config:     source.ConfigJSON,
		Credential: source.CredentialJSON,
	})
	if err != nil {
		return err
	}
	localDocs, err := s.sources.ListExternalDocumentsBySource(source.ID)
	if err != nil {
		return err
	}
	localByRemoteID := make(map[string]model.ExternalDocument, len(localDocs))
	for _, doc := range localDocs {
		localByRemoteID[doc.RemoteID] = doc
	}
	seenRemoteIDs := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		seenRemoteIDs = append(seenRemoteIDs, item.RemoteID)
		local, exists := localByRemoteID[item.RemoteID]
		if exists && !shouldFetch(local, item) {
			now := time.Now()
			local.LastSyncedAt = &now
			local.SyncStatus = model.ExternalDocumentStatusSynced
			local.LastError = ""
			if err := s.sources.UpdateExternalDocument(&local); err != nil {
				return err
			}
			continue
		}
		remote, err := connector.Fetch(ctx, FetchRequest{
			Config:     source.ConfigJSON,
			Credential: source.CredentialJSON,
			RemoteID:   item.RemoteID,
		})
		if err != nil {
			if exists {
				local.SyncStatus = model.ExternalDocumentStatusFailed
				local.LastError = err.Error()
				_ = s.sources.UpdateExternalDocument(&local)
			}
			return err
		}
		if err := s.upsertRemoteDocument(ctx, source, remote, local, exists); err != nil {
			return err
		}
	}
	if err := s.sources.MarkMissingExternalDocuments(source.ID, seenRemoteIDs); err != nil {
		return err
	}
	return s.sources.CompleteSourceSync(source.ID, source.SyncIntervalSeconds)
}

func (s *Service) upsertRemoteDocument(ctx context.Context, source *model.KnowledgeSource, remote *RemoteDocument, local model.ExternalDocument, exists bool) error {
	if remote.ContentHash == "" {
		remote.ContentHash = HashContent(remote.Markdown)
	}
	if exists && local.ContentHash == remote.ContentHash {
		now := time.Now()
		local.RemoteUpdatedAt = &remote.UpdatedAt
		local.LastSyncedAt = &now
		local.SyncStatus = model.ExternalDocumentStatusSynced
		local.LastError = ""
		return s.sources.UpdateExternalDocument(&local)
	}
	objectKey := buildObjectKey(source, remote)
	body := strings.NewReader(remote.Markdown)
	if err := s.minio.Upload(ctx, objectKey, body, int64(body.Len()), "text/markdown"); err != nil {
		return err
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
	if exists {
		doc.ID = local.DocumentID
		if err := s.docs.UpdateSyncedDocument(doc); err != nil {
			return err
		}
	} else {
		if err := s.docs.Create(doc); err != nil {
			return err
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
		SyncStatus:      model.ExternalDocumentStatusSynced,
		LastSyncedAt:    &now,
		LastError:       "",
	}
	if exists {
		external.ID = local.ID
		external.CreatedAt = local.CreatedAt
		if err := s.sources.UpdateExternalDocument(external); err != nil {
			return err
		}
	} else if err := s.sources.CreateExternalDocument(external); err != nil {
		return err
	}
	return s.tasks.PublishDocumentIndex(ctx, doc.ID, doc.UserID, doc.KnowledgeBaseID)
}

func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func shouldFetch(local model.ExternalDocument, item RemoteItem) bool {
	if item.AlwaysFetch {
		return true
	}
	if local.SyncStatus == model.ExternalDocumentStatusMissing || local.SyncStatus == model.ExternalDocumentStatusFailed {
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
