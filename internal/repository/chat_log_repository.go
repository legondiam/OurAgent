package repository

import (
	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ChatLogRepository struct {
	db *gorm.DB
}

func NewChatLogRepository(db *gorm.DB) *ChatLogRepository {
	return &ChatLogRepository{db: db}
}

// Create 创建问答日志记录
func (r *ChatLogRepository) Create(log *model.ChatLog) error {
	if err := r.db.Create(log).Error; err != nil {
		return pkgerrors.WithMessage(err, "保存问答日志失败")
	}
	return nil
}

// ListByUserID 查询用户问答日志列表
func (r *ChatLogRepository) ListByUserID(userID uint64, limit int) ([]model.ChatLog, error) {
	var logs []model.ChatLog
	if err := r.db.Where("user_id = ?", userID).Order("id desc").Limit(limit).Find(&logs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询问答日志失败")
	}
	return logs, nil
}

// FindByIDAndUserID 查询用户问答日志
func (r *ChatLogRepository) FindByIDAndUserID(id, userID uint64) (*model.ChatLog, error) {
	var log model.ChatLog
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&log).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询问答日志失败")
	}
	return &log, nil
}

// UpsertFeedback 创建或更新问答反馈
func (r *ChatLogRepository) UpsertFeedback(feedback *model.ChatFeedback) error {
	if err := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chat_log_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"rating", "reason", "updated_at"}),
	}).Create(feedback).Error; err != nil {
		return pkgerrors.WithMessage(err, "保存问答反馈失败")
	}
	return nil
}
