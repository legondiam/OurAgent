package repository

import (
	"errors"
	"sort"

	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ChatLogRepository struct {
	db *gorm.DB
}

// ListAfterIDByConversation查询摘要游标之后的完整问答
func (r *ChatLogRepository) ListAfterIDByConversation(userID, knowledgeBaseID uint64, conversationID string, afterID uint64) ([]model.ChatLog, error) {
	var logs []model.ChatLog
	if err := r.db.Where("user_id = ? AND knowledge_base_id = ? AND conversation_id = ? AND id > ?", userID, knowledgeBaseID, conversationID, afterID).
		Order("id asc").Find(&logs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询会话增量日志失败")
	}
	return logs, nil
}

// ListRangeByConversation查询会话指定日志范围
func (r *ChatLogRepository) ListRangeByConversation(userID, knowledgeBaseID uint64, conversationID string, afterID, throughID uint64) ([]model.ChatLog, error) {
	var logs []model.ChatLog
	if err := r.db.Where("user_id = ? AND knowledge_base_id = ? AND conversation_id = ? AND id > ? AND id <= ?", userID, knowledgeBaseID, conversationID, afterID, throughID).
		Order("id asc").Find(&logs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询会话范围日志失败")
	}
	return logs, nil
}

// FindManyOwnedByConversation批量查询当前会话日志
func (r *ChatLogRepository) FindManyOwnedByConversation(userID, knowledgeBaseID uint64, conversationID string, ids []uint64) ([]model.ChatLog, error) {
	if len(ids) == 0 {
		return []model.ChatLog{}, nil
	}
	var logs []model.ChatLog
	if err := r.db.Where("user_id = ? AND knowledge_base_id = ? AND conversation_id = ? AND id IN ?", userID, knowledgeBaseID, conversationID, ids).
		Find(&logs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "批量查询会话日志失败")
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].ID < logs[j].ID })
	return logs, nil
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

// ListRecentByConversation查询会话最近问答日志
func (r *ChatLogRepository) ListRecentByConversation(userID, knowledgeBaseID uint64, conversationID string, limit int) ([]model.ChatLog, error) {
	var logs []model.ChatLog
	if err := r.db.Where("user_id = ? AND knowledge_base_id = ? AND conversation_id = ?", userID, knowledgeBaseID, conversationID).
		Order("created_at desc").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询会话问答日志失败")
	}
	return logs, nil
}

// FindLatestByConversation 查询会话最近一条问答日志
func (r *ChatLogRepository) FindLatestByConversation(userID, knowledgeBaseID uint64, conversationID string) (*model.ChatLog, error) {
	var log model.ChatLog
	if err := r.db.Where("user_id = ? AND knowledge_base_id = ? AND conversation_id = ?", userID, knowledgeBaseID, conversationID).
		Order("created_at desc").
		First(&log).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, pkgerrors.WithMessage(err, "查询会话最近问答日志失败")
	}
	return &log, nil
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
