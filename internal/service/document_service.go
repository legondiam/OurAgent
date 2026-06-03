package service

import (
	"context"
	"fmt"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/document"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	appsearch "OurAgent/internal/search"
	"OurAgent/internal/storage"
	"OurAgent/internal/vectorstore"

	pkgerrors "github.com/pkg/errors"
)

type DocumentService struct {
	docs    *repository.DocumentRepository
	chunks  *repository.ChunkRepository
	kbs     *repository.KnowledgeBaseRepository
	indexer *document.Indexer
	qdrant  *vectorstore.QdrantClient
	keyword appsearch.KeywordStore
	minio   *storage.MinIOClient
	cfg     *config.Config
}

type UploadDocumentInput struct {
	Context context.Context
	UserID  uint64
	KBID    uint64
	File    *multipart.FileHeader
}

func NewDocumentService(docs *repository.DocumentRepository, chunks *repository.ChunkRepository, kbs *repository.KnowledgeBaseRepository, indexer *document.Indexer, qdrant *vectorstore.QdrantClient, keyword appsearch.KeywordStore, minio *storage.MinIOClient, cfg *config.Config) *DocumentService {
	return &DocumentService{docs: docs, chunks: chunks, kbs: kbs, indexer: indexer, qdrant: qdrant, keyword: keyword, minio: minio, cfg: cfg}
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

	s.indexer.IndexAsync(doc.ID)
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
	}
	if err := s.qdrant.DeleteByDocument(ctx, doc.UserID, doc.KnowledgeBaseID, doc.ID); err != nil {
		return pkgerrors.WithMessage(err, "删除向量索引失败")
	}
	if s.keyword != nil {
		if err := s.keyword.DeleteByDocumentID(ctx, doc.UserID, doc.ID); err != nil {
			return pkgerrors.WithMessage(err, "删除关键词索引失败")
		}
	}
	if err := s.chunks.DeleteByDocumentID(userID, docID); err != nil {
		return pkgerrors.WithMessage(err, "删除文档切片失败")
	}
	if err := s.minio.DeleteObject(ctx, doc.ObjectKey); err != nil {
		return err
	}
	if err := s.docs.DeleteByIDAndUserID(docID, userID); err != nil {
		return pkgerrors.WithMessage(err, "删除文档记录失败")
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
	s.indexer.IndexAsync(doc.ID)
	return doc, nil
}

func isDocumentIndexing(status string) bool {
	return status == model.DocumentStatusPending || status == model.DocumentStatusProcessing
}
