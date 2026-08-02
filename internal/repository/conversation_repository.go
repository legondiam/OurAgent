package repository

import (
	"errors"
	"time"

	"OurAgent/internal/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrConversationProcessingLeaseLost = errors.New("会话处理租约已失效")
	ErrConversationSummaryLeaseLost    = errors.New("会话摘要租约已失效")
	ErrConversationSummaryLeaseActive  = errors.New("会话摘要租约仍然有效")
)

type ConversationRepository struct {
	db *gorm.DB
}

type ConversationCompactionTask struct {
	TaskID             string
	ConversationID     string
	UserID             uint64
	KnowledgeBaseID    uint64
	SnapshotLastLogID  uint64
	BaseSummaryVersion uint64
	Attempt            int
}

func NewConversationRepository(db *gorm.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

// FindOwned查询用户知识库中的会话
func (r *ConversationRepository) FindOwned(userID, knowledgeBaseID uint64, conversationID string) (*model.Conversation, error) {
	var conversation model.Conversation
	err := r.db.Where("id = ? AND user_id = ? AND knowledge_base_id = ?", conversationID, userID, knowledgeBaseID).
		First(&conversation).Error
	if err != nil {
		return nil, err
	}
	return &conversation, nil
}

// TryAcquireProcessingLease尝试抢占会话处理租约
func (r *ConversationRepository) TryAcquireProcessingLease(userID, knowledgeBaseID uint64, conversationID, token string, now, leaseUntil time.Time) (bool, error) {
	result := r.db.Model(&model.Conversation{}).
		Where("id = ? AND user_id = ? AND knowledge_base_id = ?", conversationID, userID, knowledgeBaseID).
		Where("status = ? AND expires_at > ?", model.ConversationStatusActive, now).
		Where("processing_token = '' OR processing_token IS NULL OR processing_lease_until IS NULL OR processing_lease_until < ?", now).
		Updates(map[string]any{
			"processing_token":       token,
			"processing_lease_until": leaseUntil,
		})
	return result.RowsAffected == 1, result.Error
}

// ReleaseProcessingLease释放当前请求持有的会话租约
func (r *ConversationRepository) ReleaseProcessingLease(conversationID, token string) error {
	return r.db.Model(&model.Conversation{}).
		Where("id = ? AND processing_token = ?", conversationID, token).
		Updates(map[string]any{
			"processing_token":       "",
			"processing_lease_until": nil,
		}).Error
}

// CreateWithFirstLog创建会话并保存首轮日志
func (r *ConversationRepository) CreateWithFirstLog(conversation *model.Conversation, log *model.ChatLog) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(conversation).Error; err != nil {
			return err
		}
		return tx.Create(log).Error
	})
}

// AppendLogAndRefresh保存问答并刷新会话状态
func (r *ConversationRepository) AppendLogAndRefresh(log *model.ChatLog, processingToken string, expiresAt time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(log).Error; err != nil {
			return err
		}
		result := tx.Model(&model.Conversation{}).
			Where("id = ? AND user_id = ? AND knowledge_base_id = ? AND processing_token = ?", log.ConversationID, log.UserID, log.KnowledgeBaseID, processingToken).
			Updates(map[string]any{
				"unsummarized_tokens":    gorm.Expr("unsummarized_tokens + ?", log.ConversationTokens),
				"last_message_at":        log.CreatedAt,
				"expires_at":             expiresAt,
				"processing_token":       "",
				"processing_lease_until": nil,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConversationProcessingLeaseLost
		}
		return nil
	})
}

// TryQueueCompaction创建唯一会话摘要任务
func (r *ConversationRepository) TryQueueCompaction(userID, knowledgeBaseID uint64, conversationID, taskID string, triggerTokens int, now, queueLeaseUntil time.Time) (*ConversationCompactionTask, error) {
	var task *ConversationCompactionTask
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var conversation model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND knowledge_base_id = ?", conversationID, userID, knowledgeBaseID).First(&conversation).Error; err != nil {
			return err
		}
		if conversation.Status != model.ConversationStatusActive || !conversation.ExpiresAt.After(now) || conversation.UnsummarizedTokens < triggerTokens {
			return nil
		}
		if (conversation.SummaryStatus == model.ConversationSummaryStatusQueued || conversation.SummaryStatus == model.ConversationSummaryStatusProcessing) &&
			conversation.SummaryLeaseUntil != nil && conversation.SummaryLeaseUntil.After(now) {
			return nil
		}
		var latestID uint64
		if err := tx.Model(&model.ChatLog{}).
			Where("user_id = ? AND knowledge_base_id = ? AND conversation_id = ?", userID, knowledgeBaseID, conversationID).
			Select("COALESCE(MAX(id), 0)").Scan(&latestID).Error; err != nil {
			return err
		}
		if latestID <= conversation.SummarizedThroughID {
			return nil
		}
		if err := tx.Model(&model.Conversation{}).Where("id = ?", conversationID).Updates(map[string]any{
			"summary_status":      model.ConversationSummaryStatusQueued,
			"summary_task_id":     taskID,
			"summary_attempt":     0,
			"summary_lease_until": queueLeaseUntil,
			"last_summary_error":  "",
		}).Error; err != nil {
			return err
		}
		task = &ConversationCompactionTask{
			TaskID:             taskID,
			ConversationID:     conversationID,
			UserID:             userID,
			KnowledgeBaseID:    knowledgeBaseID,
			SnapshotLastLogID:  latestID,
			BaseSummaryVersion: conversation.SummaryVersion,
		}
		return nil
	})
	return task, err
}

// ClaimCompaction抢占会话摘要任务
func (r *ConversationRepository) ClaimCompaction(task ConversationCompactionTask, attempt int, now, leaseUntil time.Time) (bool, error) {
	result := r.db.Model(&model.Conversation{}).
		Where("id = ? AND user_id = ? AND knowledge_base_id = ? AND summary_task_id = ?", task.ConversationID, task.UserID, task.KnowledgeBaseID, task.TaskID).
		Where("summary_status IN ? OR (summary_status = ? AND (summary_lease_until IS NULL OR summary_lease_until < ?))", []string{model.ConversationSummaryStatusQueued, model.ConversationSummaryStatusFailed}, model.ConversationSummaryStatusProcessing, now).
		Updates(map[string]any{
			"summary_status":      model.ConversationSummaryStatusProcessing,
			"summary_attempt":     attempt,
			"summary_lease_until": leaseUntil,
		})
	return result.RowsAffected == 1, result.Error
}

// CompleteCompaction提交新的摘要和压缩游标
func (r *ConversationRepository) CompleteCompaction(task ConversationCompactionTask, summary datatypes.JSON, summarizedThroughID uint64, compactedTokens int) error {
	result := r.db.Model(&model.Conversation{}).
		Where("id = ? AND summary_task_id = ? AND summary_status = ? AND summary_version = ?", task.ConversationID, task.TaskID, model.ConversationSummaryStatusProcessing, task.BaseSummaryVersion).
		Updates(map[string]any{
			"summary_json":           summary,
			"summary_schema_version": 1,
			"summarized_through_id":  summarizedThroughID,
			"unsummarized_tokens":    gorm.Expr("GREATEST(0, unsummarized_tokens - ?)", compactedTokens),
			"summary_version":        gorm.Expr("summary_version + 1"),
			"summary_status":         model.ConversationSummaryStatusIdle,
			"summary_task_id":        "",
			"summary_attempt":        0,
			"summary_lease_until":    nil,
			"last_summary_error":     "",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConversationSummaryLeaseLost
	}
	return nil
}

// FailCompaction记录会话摘要任务失败
func (r *ConversationRepository) FailCompaction(conversationID, taskID string, attempt int, message string) error {
	return r.db.Model(&model.Conversation{}).
		Where("id = ? AND summary_task_id = ?", conversationID, taskID).
		Updates(map[string]any{
			"summary_status":      model.ConversationSummaryStatusFailed,
			"summary_attempt":     attempt,
			"summary_lease_until": nil,
			"last_summary_error":  message,
		}).Error
}
