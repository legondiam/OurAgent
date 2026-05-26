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
