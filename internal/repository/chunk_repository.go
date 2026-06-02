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

// FindParentsByIDs 批量查询父切片
func (r *ChunkRepository) FindParentsByIDs(userID, kbID uint64, ids []uint64) ([]model.DocumentParentChunk, error) {
	var chunks []model.DocumentParentChunk
	if len(ids) == 0 {
		return chunks, nil
	}
	if err := r.db.Where("id IN ? AND user_id = ? AND knowledge_base_id = ?", ids, userID, kbID).Find(&chunks).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询父文档切片失败")
	}
	return chunks, nil
}

// FindChildrenByIDs 批量查询子切片
func (r *ChunkRepository) FindChildrenByIDs(userID, kbID uint64, ids []uint64) ([]model.DocumentChildChunk, error) {
	var chunks []model.DocumentChildChunk
	if len(ids) == 0 {
		return chunks, nil
	}
	if err := r.db.Where("id IN ? AND user_id = ? AND knowledge_base_id = ?", ids, userID, kbID).Find(&chunks).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询子文档切片失败")
	}
	return chunks, nil
}

// DeleteByDocumentID 删除文档下的全部切片
func (r *ChunkRepository) DeleteByDocumentID(userID, documentID uint64) error {
	if err := r.db.Where("document_id = ? AND user_id = ?", documentID, userID).Delete(&model.DocumentChildChunk{}).Error; err != nil {
		return pkgerrors.WithMessage(err, "删除子文档切片失败")
	}
	if err := r.db.Where("document_id = ? AND user_id = ?", documentID, userID).Delete(&model.DocumentParentChunk{}).Error; err != nil {
		return pkgerrors.WithMessage(err, "删除父文档切片失败")
	}
	return nil
}
