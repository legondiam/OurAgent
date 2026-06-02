package repository

import (
	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
)

type DocumentRepository struct {
	db *gorm.DB
}

func NewDocumentRepository(db *gorm.DB) *DocumentRepository {
	return &DocumentRepository{db: db}
}

// Create 创建文档记录
func (r *DocumentRepository) Create(doc *model.Document) error {
	if err := r.db.Create(doc).Error; err != nil {
		return pkgerrors.WithMessage(err, "保存文档记录失败")
	}
	return nil
}

// ListByKnowledgeBase 查询知识库文档列表
func (r *DocumentRepository) ListByKnowledgeBase(userID, kbID uint64) ([]model.Document, error) {
	var docs []model.Document
	if err := r.db.Where("knowledge_base_id = ? AND user_id = ?", kbID, userID).Order("id desc").Find(&docs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档失败")
	}
	return docs, nil
}

// FindByIDAndUserID 按文档 ID 和用户 ID 查询文档
func (r *DocumentRepository) FindByIDAndUserID(id, userID uint64) (*model.Document, error) {
	var doc model.Document
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&doc).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档失败")
	}
	return &doc, nil
}

// CountCompleted 统计已完成索引的文档数量
func (r *DocumentRepository) CountCompleted(userID, kbID uint64) (int64, error) {
	var count int64
	if err := r.db.Model(&model.Document{}).
		Where("knowledge_base_id = ? AND user_id = ? AND status = ?", kbID, userID, "completed").
		Count(&count).Error; err != nil {
		return 0, pkgerrors.WithMessage(err, "统计已完成索引文档失败")
	}
	return count, nil
}

// FindByIDsAndUserID 批量查询用户文档
func (r *DocumentRepository) FindByIDsAndUserID(ids []uint64, userID uint64) ([]model.Document, error) {
	var docs []model.Document
	if len(ids) == 0 {
		return docs, nil
	}
	if err := r.db.Where("id IN ? AND user_id = ?", ids, userID).Find(&docs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "批量查询文档失败")
	}
	return docs, nil
}

// DeleteByIDAndUserID 删除用户文档记录
func (r *DocumentRepository) DeleteByIDAndUserID(id, userID uint64) error {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.Document{})
	if result.Error != nil {
		return pkgerrors.WithMessage(result.Error, "删除文档失败")
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateStatus 更新文档索引状态
func (r *DocumentRepository) UpdateStatus(id, userID uint64, status, message string, chunkCount int) error {
	updates := map[string]interface{}{
		"status":        status,
		"error_message": message,
		"chunk_count":   chunkCount,
	}
	if err := r.db.Model(&model.Document{}).Where("id = ? AND user_id = ?", id, userID).Updates(updates).Error; err != nil {
		return pkgerrors.WithMessage(err, "更新文档状态失败")
	}
	return nil
}
