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
func (r *SourceRepository) MarkSourceQueued(id, userID uint64) (bool, error) {
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND user_id = ? AND enabled = ? AND sync_status IN ?", id, userID, true, []string{
			model.KnowledgeSourceStatusIdle,
			model.KnowledgeSourceStatusFailed,
		}).
		Updates(map[string]interface{}{
			"sync_status": model.KnowledgeSourceStatusQueued,
			"last_error":  "",
		})
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "更新知识源同步状态失败")
	}
	return result.RowsAffected > 0, nil
}

// MarkSourceSyncing抢占知识源同步执行状态
func (r *SourceRepository) MarkSourceSyncing(id uint64) (bool, error) {
	result := r.db.Model(&model.KnowledgeSource{}).
		Where("id = ? AND enabled = ? AND sync_status = ?", id, true, model.KnowledgeSourceStatusQueued).
		Updates(map[string]interface{}{
			"sync_status": model.KnowledgeSourceStatusSyncing,
			"last_error":  "",
		})
	if result.Error != nil {
		return false, pkgerrors.WithMessage(result.Error, "更新知识源同步状态失败")
	}
	return result.RowsAffected > 0, nil
}

// CompleteSourceSync完成知识源同步状态
func (r *SourceRepository) CompleteSourceSync(id uint64, intervalSeconds int) error {
	now := time.Now()
	var next *time.Time
	if intervalSeconds > 0 {
		nextTime := now.Add(time.Duration(intervalSeconds) * time.Second)
		next = &nextTime
	}
	if err := r.db.Model(&model.KnowledgeSource{}).Where("id = ?", id).Updates(map[string]interface{}{
		"sync_status":  model.KnowledgeSourceStatusIdle,
		"last_sync_at": &now,
		"next_sync_at": next,
		"last_error":   "",
	}).Error; err != nil {
		return pkgerrors.WithMessage(err, "完成知识源同步状态失败")
	}
	return nil
}

// FailSourceSync记录知识源同步失败状态
func (r *SourceRepository) FailSourceSync(id uint64, message string) error {
	if err := r.db.Model(&model.KnowledgeSource{}).Where("id = ?", id).Updates(map[string]interface{}{
		"sync_status": model.KnowledgeSourceStatusFailed,
		"last_error":  message,
	}).Error; err != nil {
		return pkgerrors.WithMessage(err, "记录知识源同步失败状态失败")
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

// MarkMissingExternalDocuments标记本次未发现的远程文档
func (r *SourceRepository) MarkMissingExternalDocuments(sourceID uint64, seenRemoteIDs []string) error {
	query := r.db.Model(&model.ExternalDocument{}).Where("source_id = ?", sourceID)
	if len(seenRemoteIDs) > 0 {
		query = query.Where("remote_id NOT IN ?", seenRemoteIDs)
	}
	if err := query.Updates(map[string]interface{}{
		"sync_status": model.ExternalDocumentStatusMissing,
	}).Error; err != nil {
		return pkgerrors.WithMessage(err, "标记缺失外部文档失败")
	}
	return nil
}
