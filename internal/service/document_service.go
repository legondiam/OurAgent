package service

import (
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

	pkgerrors "github.com/pkg/errors"
)

type DocumentService struct {
	docs    *repository.DocumentRepository
	kbs     *repository.KnowledgeBaseRepository
	indexer *document.Indexer
	cfg     *config.Config
}

type UploadDocumentInput struct {
	UserID uint64
	KBID   uint64
	File   *multipart.FileHeader
	Save   func(file *multipart.FileHeader, dst string) error
}

func NewDocumentService(docs *repository.DocumentRepository, kbs *repository.KnowledgeBaseRepository, indexer *document.Indexer, cfg *config.Config) *DocumentService {
	return &DocumentService{docs: docs, kbs: kbs, indexer: indexer, cfg: cfg}
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
