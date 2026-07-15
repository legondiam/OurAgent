package repository

import (
	"time"

	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
)

type SourceRepository struct {
	db *gorm.DB
}

// NewSourceRepository创建知识源仓储
func NewSourceRepository(db *gorm.DB) *SourceRepository {
	return &SourceRepository{db: db}
}

// CreateSource创建知识源记录
func (r *SourceRepository) CreateSource(source *model.KnowledgeSource) error {
	if err := r.db.Create(source).Error; err != nil {
		return pkgerrors.WithMessage(err, "保存知识源失败")
	}
	return nil
}

// ListSources查询知识库下的知识源
func (r *SourceRepository) ListSources(userID, kbID uint64) ([]model.KnowledgeSource, error) {
	var sources []model.KnowledgeSource
	if err := r.db.Where("user_id = ? AND knowledge_base_id = ?", userID, kbID).Order("id desc").Find(&sources).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识源失败")
	}
	return sources, nil
}

// FindSourceByID按ID查询知识源
func (r *SourceRepository) FindSourceByID(id uint64) (*model.KnowledgeSource, error) {
	var source model.KnowledgeSource
	if err := r.db.Where("id = ?", id).First(&source).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识源失败")
	}
	return &source, nil
}

// FindSourceByIDAndUserID按ID和用户查询知识源
func (r *SourceRepository) FindSourceByIDAndUserID(id, userID uint64) (*model.KnowledgeSource, error) {
	var source model.KnowledgeSource
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&source).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识源失败")
	}
	return &source, nil
}

// MarkSourceQueued抢占知识源同步排队状态
func (r *SourceRepository) MarkSourceQueued(id, userID uint64, taskID string, leaseUntil time.Time) (bool, error) {
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND user_id = ? AND enabled = ? AND sync_status IN ?", id, userID, true, []string{
			model.KnowledgeSourceStatusIdle,
			model.KnowledgeSourceStatusFailed,
		}).
		Updates(map[string]interface{}{
			"sync_status":      model.KnowledgeSourceStatusQueued,
			"sync_task_id":     taskID,
			"sync_attempt":     0,
			"sync_lease_until": &leaseUntil,
			"last_error":       "",
		})
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "更新知识源同步状态失败")
	}
	return result.RowsAffected > 0, nil
}

// MarkSourceSyncing抢占知识源同步执行状态
func (r *SourceRepository) MarkSourceSyncing(id uint64, taskID string, attempt int, leaseUntil time.Time) (bool, error) {
	now := time.Now()
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND enabled = ? AND sync_status = ? AND sync_task_id = ?", id, true, model.KnowledgeSourceStatusQueued, taskID).
		Updates(map[string]interface{}{
			"sync_status":      model.KnowledgeSourceStatusSyncing,
			"sync_attempt":     attempt,
			"sync_started_at":  &now,
			"sync_lease_until": &leaseUntil,
			"last_error":       "",
		})
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "更新知识源同步状态失败")
	}
	return result.RowsAffected > 0, nil
}

// CompleteSourceSync完成知识源同步状态
func (r *SourceRepository) CompleteSourceSync(id uint64, taskID string, intervalSeconds int, stats model.SourceSyncStats) (bool, error) {
	now := time.Now()
	var next *time.Time
	if intervalSeconds > 0 {
		nextTime := now.Add(time.Duration(intervalSeconds) * time.Second)
		next = &nextTime
	}
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND enabled = ? AND sync_status = ? AND sync_task_id = ?", id, true, model.KnowledgeSourceStatusSyncing, taskID).
		Updates(sourceCompletionUpdates(now, next, model.KnowledgeSourceStatusIdle, "", stats))
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "完成知识源同步状态失败")
	}
	return result.RowsAffected > 0, nil
}

// RequeueSourceSync记录可重试失败
func (r *SourceRepository) RequeueSourceSync(id uint64, taskID string, attempt int, message string, leaseUntil time.Time, stats model.SourceSyncStats) (bool, error) {
	now := time.Now()
	updates := sourceCompletionUpdates(now, nil, model.KnowledgeSourceStatusQueued, message, stats)
	updates["sync_attempt"] = attempt
	updates["sync_lease_until"] = &leaseUntil
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND enabled = ? AND sync_status = ? AND sync_task_id = ?", id, true, model.KnowledgeSourceStatusSyncing, taskID).
		Updates(updates)
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "记录知识源同步重试状态失败")
	}
	return result.RowsAffected > 0, nil
}

// FailSourceSync记录知识源最终失败状态
func (r *SourceRepository) FailSourceSync(id uint64, taskID string, attempt int, message string, stats model.SourceSyncStats) (bool, error) {
	now := time.Now()
	updates := sourceCompletionUpdates(now, nil, model.KnowledgeSourceStatusFailed, message, stats)
	updates["sync_attempt"] = attempt
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND sync_status IN ? AND sync_task_id = ?", id, []string{
			model.KnowledgeSourceStatusQueued,
			model.KnowledgeSourceStatusSyncing,
		}, taskID).
		Updates(updates)
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "记录知识源同步失败状态失败")
	}
	return result.RowsAffected > 0, nil
}

// ListDueSources查询到期知识源
func (r *SourceRepository) ListDueSources(now time.Time, limit int) ([]model.KnowledgeSource, error) {
	var sources []model.KnowledgeSource
	if limit <= 0 {
		limit = 100
	}
	if err := r.db.Where("enabled = ? AND sync_status = ? AND next_sync_at IS NOT NULL AND next_sync_at <= ?", true, model.KnowledgeSourceStatusIdle, now).
		Order("next_sync_at asc").Limit(limit).Find(&sources).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询到期知识源失败")
	}
	return sources, nil
}

// ClaimDueSource抢占到期知识源
func (r *SourceRepository) ClaimDueSource(id uint64, now time.Time, taskID string, leaseUntil time.Time) (bool, error) {
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND enabled = ? AND sync_status = ? AND next_sync_at IS NOT NULL AND next_sync_at <= ?", id, true, model.KnowledgeSourceStatusIdle, now).
		Updates(map[string]interface{}{
			"sync_status":      model.KnowledgeSourceStatusQueued,
			"sync_task_id":     taskID,
			"sync_attempt":     0,
			"sync_lease_until": &leaseUntil,
			"last_error":       "",
		})
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "抢占到期知识源失败")
	}
	return result.RowsAffected > 0, nil
}

// RestoreScheduledSource恢复调度投递失败状态
func (r *SourceRepository) RestoreScheduledSource(id uint64, taskID, message string) error {
	if err := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND sync_status = ? AND sync_task_id = ?", id, model.KnowledgeSourceStatusQueued, taskID).
		Updates(map[string]interface{}{
			"sync_status":      model.KnowledgeSourceStatusIdle,
			"sync_task_id":     "",
			"sync_attempt":     0,
			"sync_lease_until": nil,
			"last_error":       message,
		}).Error; err != nil {
		return pkgerrors.WithMessage(err, "恢复知识源调度状态失败")
	}
	return nil
}

// ListExpiredSources查询租约过期任务
func (r *SourceRepository) ListExpiredSources(now time.Time, limit int) ([]model.KnowledgeSource, error) {
	var sources []model.KnowledgeSource
	if limit <= 0 {
		limit = 100
	}
	if err := r.db.Where("enabled = ? AND sync_status IN ? AND sync_lease_until IS NOT NULL AND sync_lease_until <= ?", true, []string{
		model.KnowledgeSourceStatusQueued,
		model.KnowledgeSourceStatusSyncing,
	}, now).Order("sync_lease_until asc").Limit(limit).Find(&sources).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询租约过期知识源失败")
	}
	return sources, nil
}

// RecoverExpiredSource抢占租约过期任务
func (r *SourceRepository) RecoverExpiredSource(id uint64, oldTaskID, newTaskID string, now, leaseUntil time.Time) (bool, error) {
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND enabled = ? AND sync_status IN ? AND sync_task_id = ? AND sync_lease_until IS NOT NULL AND sync_lease_until <= ?", id, true, []string{
			model.KnowledgeSourceStatusQueued,
			model.KnowledgeSourceStatusSyncing,
		}, oldTaskID, now).
		Updates(map[string]interface{}{
			"sync_status":      model.KnowledgeSourceStatusQueued,
			"sync_task_id":     newTaskID,
			"sync_lease_until": &leaseUntil,
			"last_error":       "同步任务租约过期，等待恢复",
		})
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "恢复租约过期知识源失败")
	}
	return result.RowsAffected > 0, nil
}

// MarkRecoveryPublishFailed记录恢复任务投递失败
func (r *SourceRepository) MarkRecoveryPublishFailed(id uint64, taskID, message string, retryAt time.Time) error {
	if err := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND sync_status = ? AND sync_task_id = ?", id, model.KnowledgeSourceStatusQueued, taskID).
		Updates(map[string]interface{}{
			"sync_lease_until": &retryAt,
			"last_error":       message,
		}).Error; err != nil {
		return pkgerrors.WithMessage(err, "记录知识源恢复投递失败状态失败")
	}
	return nil
}

// UpdateSourceCredential更新知识源授权凭据
func (r *SourceRepository) UpdateSourceCredential(id, userID uint64, credential []byte) error {
	if err := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"credential_json": credential,
			"last_error":      "",
		}).Error; err != nil {
		return pkgerrors.WithMessage(err, "更新知识源授权凭据失败")
	}
	return nil
}

// ListExternalDocumentsBySource查询知识源文档映射
func (r *SourceRepository) ListExternalDocumentsBySource(sourceID uint64) ([]model.ExternalDocument, error) {
	var docs []model.ExternalDocument
	if err := r.db.Where("source_id = ?", sourceID).Find(&docs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询外部文档映射失败")
	}
	return docs, nil
}

// ListExternalDocumentsForUser查询用户可见的文档映射
func (r *SourceRepository) ListExternalDocumentsForUser(sourceID, userID uint64) ([]model.ExternalDocument, error) {
	var docs []model.ExternalDocument
	if err := r.db.Where("source_id = ? AND user_id = ?", sourceID, userID).Order("id desc").Find(&docs).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询外部文档映射失败")
	}
	return docs, nil
}

// CreateExternalDocument创建外部文档映射
func (r *SourceRepository) CreateExternalDocument(doc *model.ExternalDocument) error {
	if err := r.db.Create(doc).Error; err != nil {
		return pkgerrors.WithMessage(err, "保存外部文档映射失败")
	}
	return nil
}

// UpdateExternalDocument更新外部文档映射
func (r *SourceRepository) UpdateExternalDocument(doc *model.ExternalDocument) error {
	if err := r.db.Save(doc).Error; err != nil {
		return pkgerrors.WithMessage(err, "更新外部文档映射失败")
	}
	return nil
}

// FindExternalDocumentByID按ID查询外部文档映射
func (r *SourceRepository) FindExternalDocumentByID(id uint64) (*model.ExternalDocument, error) {
	var doc model.ExternalDocument
	if err := r.db.Where("id = ?", id).First(&doc).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询外部文档映射失败")
	}
	return &doc, nil
}

// MarkExternalDocumentSynced标记外部文档索引完成
func (r *SourceRepository) MarkExternalDocumentSynced(id, documentID uint64) error {
	now := time.Now()
	if err := r.db.Model(&model.ExternalDocument{}).Where("id = ? AND document_id = ?", id, documentID).Updates(map[string]interface{}{
		"sync_status":     model.ExternalDocumentStatusSynced,
		"missing_count":   0,
		"missing_task_id": "",
		"last_missing_at": nil,
		"last_synced_at":  &now,
		"last_error":      "",
	}).Error; err != nil {
		return pkgerrors.WithMessage(err, "更新外部文档同步状态失败")
	}
	return nil
}

// MarkExternalDocumentDeindexed标记外部文档已下线索引
func (r *SourceRepository) MarkExternalDocumentDeindexed(id, documentID uint64) error {
	if err := r.db.Model(&model.ExternalDocument{}).
		Where("id = ? AND document_id = ? AND sync_status <> ?", id, documentID, model.ExternalDocumentStatusDeleted).
		Updates(map[string]interface{}{
			"sync_status": model.ExternalDocumentStatusMissing,
			"last_error":  "",
		}).Error; err != nil {
		return pkgerrors.WithMessage(err, "更新外部文档下线状态失败")
	}
	return nil
}

// MarkExternalDocumentFailed记录外部文档失败
func (r *SourceRepository) MarkExternalDocumentFailed(id, documentID uint64, message string) error {
	if id == 0 {
		return nil
	}
	if err := r.db.Model(&model.ExternalDocument{}).Where("id = ? AND document_id = ? AND sync_status <> ?", id, documentID, model.ExternalDocumentStatusDeleted).Updates(map[string]interface{}{
		"sync_status": model.ExternalDocumentStatusFailed,
		"last_error":  message,
	}).Error; err != nil {
		return pkgerrors.WithMessage(err, "记录外部文档失败状态失败")
	}
	return nil
}

// MarkExternalDocumentMissing累计远端缺失次数
func (r *SourceRepository) MarkExternalDocumentMissing(id uint64, taskID string) (*model.ExternalDocument, error) {
	now := time.Now()
	result := r.db.Model(&model.ExternalDocument{}).
		Where("id = ? AND sync_status <> ? AND (missing_task_id IS NULL OR missing_task_id = '' OR missing_task_id <> ?)", id, model.ExternalDocumentStatusDeleted, taskID).
		Updates(map[string]interface{}{
			"sync_status":     model.ExternalDocumentStatusMissing,
			"missing_count":   gorm.Expr("missing_count + 1"),
			"missing_task_id": taskID,
			"last_missing_at": &now,
			"last_error":      "",
		})
	if result.Error != nil {
		return nil, pkgerrors.WithMessage(result.Error, "更新外部文档缺失状态失败")
	}
	return r.FindExternalDocumentByID(id)
}

// MarkExternalDocumentDeleted标记外部文档已删除
func (r *SourceRepository) MarkExternalDocumentDeleted(id, documentID uint64) error {
	if id == 0 {
		return nil
	}
	if err := r.db.Model(&model.ExternalDocument{}).Where("id = ? AND document_id = ?", id, documentID).Updates(map[string]interface{}{
		"sync_status": model.ExternalDocumentStatusDeleted,
		"last_error":  "",
	}).Error; err != nil {
		return pkgerrors.WithMessage(err, "更新外部文档删除状态失败")
	}
	return nil
}

func sourceCompletionUpdates(now time.Time, next *time.Time, status, message string, stats model.SourceSyncStats) map[string]interface{} {
	updates := map[string]interface{}{
		"sync_status":           status,
		"sync_finished_at":      &now,
		"sync_lease_until":      nil,
		"last_sync_duration_ms": stats.DurationMS,
		"last_scan_count":       stats.ScanCount,
		"last_created_count":    stats.CreatedCount,
		"last_updated_count":    stats.UpdatedCount,
		"last_unchanged_count":  stats.UnchangedCount,
		"last_missing_count":    stats.MissingCount,
		"last_deleted_count":    stats.DeletedCount,
		"last_failed_count":     stats.FailedCount,
		"last_error":            message,
	}
	if status == model.KnowledgeSourceStatusIdle {
		updates["last_sync_at"] = &now
		updates["next_sync_at"] = next
	}
	return updates
}
