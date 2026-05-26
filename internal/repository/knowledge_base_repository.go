package repository

import (
	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
)

type KnowledgeBaseRepository struct {
	db *gorm.DB
}

func NewKnowledgeBaseRepository(db *gorm.DB) *KnowledgeBaseRepository {
	return &KnowledgeBaseRepository{db: db}
}

// Create 创建知识库记录
func (r *KnowledgeBaseRepository) Create(kb *model.KnowledgeBase) error {
	if err := r.db.Create(kb).Error; err != nil {
		return pkgerrors.WithMessage(err, "创建知识库失败")
	}
	return nil
}

// ListByUserID 查询用户知识库列表
func (r *KnowledgeBaseRepository) ListByUserID(userID uint64) ([]model.KnowledgeBase, error) {
	var items []model.KnowledgeBase
	if err := r.db.Where("user_id = ?", userID).Order("id desc").Find(&items).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	return items, nil
}

// DeleteByIDAndUserID 删除用户知识库记录
func (r *KnowledgeBaseRepository) DeleteByIDAndUserID(id, userID uint64) (int64, error) {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.KnowledgeBase{})
	if result.Error != nil {
		return 0, pkgerrors.WithMessage(result.Error, "删除知识库失败")
	}
	return result.RowsAffected, nil
}

// ExistsByIDAndUserID 判断用户知识库是否存在
func (r *KnowledgeBaseRepository) ExistsByIDAndUserID(id, userID uint64) (bool, error) {
	var count int64
	if err := r.db.Model(&model.KnowledgeBase{}).Where("id = ? AND user_id = ?", id, userID).Count(&count).Error; err != nil {
		return false, pkgerrors.WithMessage(err, "查询知识库归属失败")
	}
	return count > 0, nil
}
