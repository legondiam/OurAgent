package repository

import (
	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
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
