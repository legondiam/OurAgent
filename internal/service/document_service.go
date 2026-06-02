package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/document"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	"OurAgent/internal/vectorstore"

	pkgerrors "github.com/pkg/errors"
)

type DocumentService struct {
	docs    *repository.DocumentRepository
	chunks  *repository.ChunkRepository
	kbs     *repository.KnowledgeBaseRepository
	indexer *document.Indexer
	qdrant  *vectorstore.QdrantClient
	cfg     *config.Config
}

type UploadDocumentInput struct {
	UserID uint64
	KBID   uint64
	File   *multipart.FileHeader
	Save   func(file *multipart.FileHeader, dst string) error
}

func NewDocumentService(docs *repository.DocumentRepository, chunks *repository.ChunkRepository, kbs *repository.KnowledgeBaseRepository, indexer *document.Indexer, qdrant *vectorstore.QdrantClient, cfg *config.Config) *DocumentService {
	return &DocumentService{docs: docs, chunks: chunks, kbs: kbs, indexer: indexer, qdrant: qdrant, cfg: cfg}
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

	if err := os.MkdirAll(s.cfg.Storage.DocumentDir, 0755); err != nil {
		return nil, pkgerrors.WithMessage(err, "创建存储目录失败")
	}
	safeName := filepath.Base(input.File.Filename)
	storedName := fmt.Sprintf("%d_%d_%s", input.UserID, time.Now().UnixNano(), safeName)
	dst := filepath.Join(s.cfg.Storage.DocumentDir, storedName)
	if err := input.Save(input.File, dst); err != nil {
		return nil, pkgerrors.WithMessage(err, "保存文件失败")
	}

	doc := &model.Document{
		KnowledgeBaseID: input.KBID,
		UserID:          input.UserID,
		Filename:        input.File.Filename,
		FileType:        ext,
		FilePath:        dst,
		Status:          "pending",
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
	if doc.Status == "processing" {
		return pkgerrors.WithStack(ErrDocumentIndexing)
	}
	if err := s.qdrant.DeleteByDocument(ctx, doc.UserID, doc.KnowledgeBaseID, doc.ID); err != nil {
		return pkgerrors.WithMessage(err, "删除向量索引失败")
	}
	if err := s.chunks.DeleteByDocumentID(userID, docID); err != nil {
		return pkgerrors.WithMessage(err, "删除文档切片失败")
	}
	if err := s.docs.DeleteByIDAndUserID(docID, userID); err != nil {
		return pkgerrors.WithMessage(err, "删除文档记录失败")
	}
	if err := os.Remove(doc.FilePath); err != nil && !stderrors.Is(err, os.ErrNotExist) {
		return pkgerrors.WithMessage(err, "删除本地文件失败")
	}
	return nil
}

// Reindex 重新触发文档索引
func (s *DocumentService) Reindex(userID, docID uint64) (*model.Document, error) {
	doc, err := s.docs.FindByIDAndUserID(docID, userID)
	if err != nil {
		return nil, pkgerrors.WithStack(ErrDocumentNotFound)
	}
	if doc.Status == "processing" {
		return nil, pkgerrors.WithStack(ErrDocumentIndexing)
	}
	if err := s.docs.UpdateStatus(doc.ID, userID, "pending", "", 0); err != nil {
		return nil, pkgerrors.WithMessage(err, "更新文档状态失败")
	}
	doc.Status = "pending"
	doc.ErrorMessage = ""
	doc.ChunkCount = 0
	s.indexer.IndexAsync(doc.ID)
	return doc, nil
}
