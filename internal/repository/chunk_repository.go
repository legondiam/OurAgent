package repository

import (
	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
)

type ChunkRepository struct {
	db *gorm.DB
}

func NewChunkRepository(db *gorm.DB) *ChunkRepository {
	return &ChunkRepository{db: db}
}

// FindByIDs 批量查询文档切片
func (r *ChunkRepository) FindByIDs(userID, kbID uint64, ids []uint64) ([]model.DocumentChunk, error) {
	var chunks []model.DocumentChunk
	if len(ids) == 0 {
		return chunks, nil
	}
	if err := r.db.Where("id IN ? AND user_id = ? AND knowledge_base_id = ?", ids, userID, kbID).Find(&chunks).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档切片失败")
	}
	return chunks, nil
}

// FindByDocumentID 查询文档下的全部切片
func (r *ChunkRepository) FindByDocumentID(userID, documentID uint64) ([]model.DocumentChunk, error) {
	var chunks []model.DocumentChunk
	if err := r.db.Where("document_id = ? AND user_id = ?", documentID, userID).Find(&chunks).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档切片失败")
	}
	return chunks, nil
}

// DeleteByDocumentID 删除文档下的全部切片
func (r *ChunkRepository) DeleteByDocumentID(userID, documentID uint64) error {
	if err := r.db.Where("document_id = ? AND user_id = ?", documentID, userID).Delete(&model.DocumentChunk{}).Error; err != nil {
		return pkgerrors.WithMessage(err, "删除文档切片失败")
	}
	return nil
}
