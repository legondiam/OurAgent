package service

import (
	"context"
	"fmt"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	"OurAgent/internal/storage"

	pkgerrors "github.com/pkg/errors"
)

type DocumentTaskPublisher interface {
	PublishDocumentIndex(ctx context.Context, documentID, userID, knowledgeBaseID uint64) error
	PublishDocumentDeleteCleanup(ctx context.Context, doc model.Document) error
}

type DocumentService struct {
	docs  *repository.DocumentRepository
	kbs   *repository.KnowledgeBaseRepository
	tasks DocumentTaskPublisher
	minio *storage.MinIOClient
}

type UploadDocumentInput struct {
	Context context.Context
	UserID  uint64
	KBID    uint64
	File    *multipart.FileHeader
}

func NewDocumentService(docs *repository.DocumentRepository, kbs *repository.KnowledgeBaseRepository, taskPublisher DocumentTaskPublisher, minio *storage.MinIOClient) *DocumentService {
	return &DocumentService{docs: docs, kbs: kbs, tasks: taskPublisher, minio: minio}
}

// Upload 保存上传文档并触发异步索引
func (s *DocumentService) Upload(input UploadDocumentInput) (*model.Document, error) {
	exists, err := s.kbs.ExistsByIDAndUserID(input.KBID, input.UserID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	if !exists {
		return nil, pkgerrors.WithStack(ErrKnowledgeBaseNotFound)
	}
	if input.File == nil {
		return nil, pkgerrors.WithStack(ErrMissingFile)
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(input.File.Filename), "."))
	if ext != "txt" && ext != "md" && ext != "pdf" {
		return nil, pkgerrors.WithStack(ErrUnsupportedFileType)
	}

	safeName := filepath.Base(input.File.Filename)
	objectKey := fmt.Sprintf("users/%d/knowledge-bases/%d/%d_%s", input.UserID, input.KBID, time.Now().UnixNano(), safeName)
	file, err := input.File.Open()
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "打开上传文件失败")
	}
	defer file.Close()

	contentType := input.File.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx := input.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.minio.Upload(ctx, objectKey, file, input.File.Size, contentType); err != nil {
		return nil, err
	}

	doc := &model.Document{
		KnowledgeBaseID: input.KBID,
		UserID:          input.UserID,
		Filename:        input.File.Filename,
		FileType:        ext,
		FilePath:        objectKey,
		BucketName:      s.minio.Bucket(),
		ObjectKey:       objectKey,
		FileSize:        input.File.Size,
		ContentType:     contentType,
		Status:          model.DocumentStatusPending,
	}
	if err := s.docs.Create(doc); err != nil {
		return nil, pkgerrors.WithMessage(err, "保存文档记录失败")
	}
	if err := s.tasks.PublishDocumentIndex(ctx, doc.ID, doc.UserID, doc.KnowledgeBaseID); err != nil {
		_ = s.docs.UpdateStatus(doc.ID, doc.UserID, model.DocumentStatusFailed, "索引任务投递失败: "+err.Error(), 0)
		return nil, pkgerrors.WithMessage(err, "投递索引任务失败")
	}
	return doc, nil
}

// List 查询知识库文档列表
func (s *DocumentService) List(userID, kbID uint64) ([]model.Document, error) {
	exists, err := s.kbs.ExistsByIDAndUserID(kbID, userID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	if !exists {
		return nil, pkgerrors.WithStack(ErrKnowledgeBaseNotFound)
	}
	docs, err := s.docs.ListByKnowledgeBase(userID, kbID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档失败")
	}
	return docs, nil
}

// Get 查询文档详情
func (s *DocumentService) Get(userID, docID uint64) (*model.Document, error) {
	doc, err := s.docs.FindByIDAndUserID(docID, userID)
	if err != nil {
		return nil, pkgerrors.WithStack(ErrDocumentNotFound)
	}
	return doc, nil
}

// Delete 删除文档和对应索引数据
func (s *DocumentService) Delete(ctx context.Context, userID, docID uint64) error {
	doc, err := s.docs.FindByIDAndUserID(docID, userID)
	if err != nil {
		return pkgerrors.WithStack(ErrDocumentNotFound)
	}
	if isDocumentIndexing(doc.Status) {
		return pkgerrors.WithStack(ErrDocumentIndexing)
	}
	if doc.Status != model.DocumentStatusDeleting {
		if err := s.docs.UpdateStatus(doc.ID, userID, model.DocumentStatusDeleting, "", doc.ChunkCount); err != nil {
			return pkgerrors.WithMessage(err, "更新文档状态失败")
		}
		doc.Status = model.DocumentStatusDeleting
	}
	if err := s.tasks.PublishDocumentDeleteCleanup(ctx, *doc); err != nil {
		return pkgerrors.WithMessage(err, "投递文档删除清理任务失败")
	}
	return nil
}

// Reindex 重新触发文档索引
func (s *DocumentService) Reindex(userID, docID uint64) (*model.Document, error) {
	doc, err := s.docs.FindByIDAndUserID(docID, userID)
	if err != nil {
		return nil, pkgerrors.WithStack(ErrDocumentNotFound)
	}
	if isDocumentIndexing(doc.Status) || doc.Status == model.DocumentStatusDeleting {
		return nil, pkgerrors.WithStack(ErrDocumentIndexing)
	}
	if err := s.docs.UpdateStatus(doc.ID, userID, model.DocumentStatusPending, "", 0); err != nil {
		return nil, pkgerrors.WithMessage(err, "更新文档状态失败")
	}
	doc.Status = model.DocumentStatusPending
	doc.ErrorMessage = ""
	doc.ChunkCount = 0
	if err := s.tasks.PublishDocumentIndex(context.Background(), doc.ID, doc.UserID, doc.KnowledgeBaseID); err != nil {
		_ = s.docs.UpdateStatus(doc.ID, userID, model.DocumentStatusFailed, "索引任务投递失败: "+err.Error(), 0)
		return nil, pkgerrors.WithMessage(err, "投递索引任务失败")
	}
	return doc, nil
}

func isDocumentIndexing(status string) bool {
	return status == model.DocumentStatusPending || status == model.DocumentStatusProcessing
}
