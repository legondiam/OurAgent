package repository

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HashMemoryText 计算长期记忆内容哈希
func HashMemoryText(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

var ErrMemoryStateChanged = errors.New("长期记忆状态已变化")

type LongTermMemoryRepository struct {
	db *gorm.DB
}

type MemoryListFilter struct {
	UserID          uint64
	KnowledgeBaseID *uint64
	Scope           string
	Type            string
	Status          string
	Page            int
	PageSize        int
}

type MemorySignalBatch struct {
	Job       model.LongTermMemoryJob
	SignalIDs []uint64
}

// NewLongTermMemoryRepository 创建长期记忆仓储
func NewLongTermMemoryRepository(db *gorm.DB) *LongTermMemoryRepository {
	return &LongTermMemoryRepository{db: db}
}

// DB 返回长期记忆仓储使用的数据库连接
func (r *LongTermMemoryRepository) DB() *gorm.DB {
	return r.db
}

// List 按用户和筛选条件查询长期记忆
func (r *LongTermMemoryRepository) List(filter MemoryListFilter) ([]model.LongTermMemory, int64, error) {
	query := r.db.Model(&model.LongTermMemory{}).Where("user_id = ?", filter.UserID)
	if filter.KnowledgeBaseID != nil {
		query = query.Where("knowledge_base_id = ?", *filter.KnowledgeBaseID)
	}
	if filter.Scope != "" {
		query = query.Where("scope = ?", filter.Scope)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	} else {
		query = query.Where("status IN ?", []string{model.MemoryStatusActive, model.MemoryStatusCandidate})
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var memories []model.LongTermMemory
	err := query.Order("updated_at DESC").Offset((page - 1) * size).Limit(size).Find(&memories).Error
	return memories, total, err
}

// FindOwned 查询用户拥有的长期记忆
func (r *LongTermMemoryRepository) FindOwned(id, userID uint64) (*model.LongTermMemory, error) {
	var memory model.LongTermMemory
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&memory).Error
	return &memory, err
}

// FindActiveByIDs 批量回查当前作用域内可用的长期记忆
func (r *LongTermMemoryRepository) FindActiveByIDs(userID, knowledgeBaseID uint64, ids []uint64) ([]model.LongTermMemory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var memories []model.LongTermMemory
	err := r.db.Where("id IN ? AND user_id = ? AND status = ?", ids, userID, model.MemoryStatusActive).
		Where("type IN ?", supportedLongTermMemoryTypes()).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		Where("(scope = ? AND knowledge_base_id = ? AND type <> ?) OR (scope = ? AND knowledge_base_id IS NULL AND type = ?)", model.MemoryScopeKnowledgeBase, knowledgeBaseID, "preference", model.MemoryScopeUserGlobal, "preference").
		Find(&memories).Error
	return memories, err
}

func supportedLongTermMemoryTypes() []string {
	return []string{"role", "preference", "business_object", "project_context", "terminology", "instruction"}
}

// FindFixedAndLexical 查询固定偏好和词面命中的术语记忆
func (r *LongTermMemoryRepository) FindFixedAndLexical(userID, knowledgeBaseID uint64, question string, limit int) ([]model.LongTermMemory, error) {
	if limit <= 0 {
		limit = 20
	}
	var memories []model.LongTermMemory
	err := r.db.Where("user_id = ? AND status = ?", userID, model.MemoryStatusActive).
		Where("expires_at IS NULL OR expires_at > ?", time.Now()).
		Where("(scope = ? AND knowledge_base_id IS NULL AND type = ?) OR (scope = ? AND knowledge_base_id = ? AND type = ? AND ? LIKE CONCAT('%', subject, '%'))", model.MemoryScopeUserGlobal, "preference", model.MemoryScopeKnowledgeBase, knowledgeBaseID, "terminology", question).
		Order("importance DESC, last_confirmed_at DESC").Limit(limit).Find(&memories).Error
	return memories, err
}

// CreateMemory 创建长期记忆、证据、版本和索引任务
func (r *LongTermMemoryRepository) CreateMemory(memory *model.LongTermMemory, evidence *model.LongTermMemoryEvidence, changeType string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(memory).Error; err != nil {
			return err
		}
		if evidence != nil {
			evidence.MemoryID = memory.ID
			if err := tx.Create(evidence).Error; err != nil {
				return err
			}
		}
		snapshot, _ := json.Marshal(memory)
		version := model.LongTermMemoryVersion{MemoryID: memory.ID, Version: memory.Version, SnapshotJSON: datatypes.JSON(snapshot), ChangeType: changeType, CreatedAt: time.Now()}
		if evidence != nil {
			version.SourceChatLogID = &evidence.ChatLogID
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		return createMemoryJob(tx, model.MemoryJobIndex, memory.UserID, memory.KnowledgeBaseID, &memory.ID, nil, map[string]any{"version": memory.Version, "embedding_hash": memory.EmbeddingHash})
	})
}

// Confirm 确认候选记忆并创建新版本和索引任务
func (r *LongTermMemoryRepository) Confirm(id, userID uint64, expiresAt *time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var memory model.LongTermMemory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).First(&memory).Error; err != nil {
			return err
		}
		if memory.Status != model.MemoryStatusCandidate {
			return ErrMemoryStateChanged
		}
		now := time.Now()
		memory.Status = model.MemoryStatusActive
		memory.LastConfirmedAt = &now
		memory.ExpiresAt = expiresAt
		memory.Version++
		memory.EmbeddingStatus = model.MemoryEmbeddingPending
		if err := tx.Save(&memory).Error; err != nil {
			return err
		}
		snapshot, _ := json.Marshal(memory)
		if err := tx.Create(&model.LongTermMemoryVersion{MemoryID: memory.ID, Version: memory.Version, SnapshotJSON: snapshot, ChangeType: "confirm", CreatedAt: now}).Error; err != nil {
			return err
		}
		return createMemoryJob(tx, model.MemoryJobIndex, memory.UserID, memory.KnowledgeBaseID, &memory.ID, nil, map[string]any{"version": memory.Version})
	})
}

// UpdateOwned 更新用户长期记忆并保存版本快照
func (r *LongTermMemoryRepository) UpdateOwned(memory *model.LongTermMemory, userID uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current model.LongTermMemory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", memory.ID, userID).First(&current).Error; err != nil {
			return err
		}
		memory.UserID = current.UserID
		memory.Version = current.Version + 1
		memory.EmbeddingStatus = model.MemoryEmbeddingPending
		if err := tx.Save(memory).Error; err != nil {
			return err
		}
		snapshot, _ := json.Marshal(memory)
		if err := tx.Create(&model.LongTermMemoryVersion{MemoryID: memory.ID, Version: memory.Version, SnapshotJSON: snapshot, ChangeType: "update", CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
		return createMemoryJob(tx, model.MemoryJobIndex, memory.UserID, memory.KnowledgeBaseID, &memory.ID, nil, map[string]any{"version": memory.Version})
	})
}

// MarkDeleting 将单条记忆标记为删除中并创建删除任务
func (r *LongTermMemoryRepository) MarkDeleting(id, userID uint64, maxChatLogID uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var memory model.LongTermMemory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).First(&memory).Error; err != nil {
			return err
		}
		if memory.Status == model.MemoryStatusDeleting {
			return nil
		}
		if err := tx.Model(&memory).Update("status", model.MemoryStatusDeleting).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.LongTermMemoryJob{}).Where("memory_id = ? AND status IN ?", id, []string{model.MemoryJobQueued, model.MemoryJobPublished}).Update("status", model.MemoryJobCancelled).Error; err != nil {
			return err
		}
		var sourceLogIDs []uint64
		if err := tx.Model(&model.LongTermMemoryEvidence{}).Where("memory_id = ?", id).Pluck("chat_log_id", &sourceLogIDs).Error; err != nil {
			return err
		}
		if len(sourceLogIDs) > 0 {
			if err := tx.Model(&model.MemoryConsolidationSignal{}).Where("chat_log_id IN ? AND status IN ?", sourceLogIDs, []string{model.MemorySignalPending, model.MemorySignalQueued, model.MemorySignalProcessing}).Update("status", model.MemorySignalCancelled).Error; err != nil {
				return err
			}
		}
		tombstone := model.LongTermMemoryForgetTombstone{UserID: userID, KnowledgeBaseID: memory.KnowledgeBaseID, IdentityHash: memory.IdentityHash, SubjectHash: HashMemoryText(memory.Subject), ForgetBeforeChatLogID: maxChatLogID, CreatedAt: time.Now()}
		if err := tx.Create(&tombstone).Error; err != nil {
			return err
		}
		return createMemoryJob(tx, model.MemoryJobDeleteVector, userID, memory.KnowledgeBaseID, &id, nil, map[string]any{"memory_id": id})
	})
}

// MarkDeletingByScope 按作用域批量启动长期记忆删除流程
func (r *LongTermMemoryRepository) MarkDeletingByScope(userID uint64, scope string, knowledgeBaseID *uint64, maxChatLogID uint64) (int, error) {
	count := 0
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if knowledgeBaseID != nil {
			if err := tx.Model(&model.MemoryConsolidationSignal{}).Where("user_id = ? AND knowledge_base_id = ? AND status IN ?", userID, *knowledgeBaseID, []string{model.MemorySignalPending, model.MemorySignalQueued, model.MemorySignalProcessing}).Updates(map[string]any{"status": model.MemorySignalCancelled}).Error; err != nil {
				return err
			}
		}
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND scope = ? AND status <> ?", userID, scope, model.MemoryStatusDeleting)
		if knowledgeBaseID != nil {
			query = query.Where("knowledge_base_id = ?", *knowledgeBaseID)
		} else if scope == model.MemoryScopeUserGlobal {
			query = query.Where("knowledge_base_id IS NULL")
		}
		var memories []model.LongTermMemory
		if err := query.Find(&memories).Error; err != nil {
			return err
		}
		for _, memory := range memories {
			if err := tx.Model(&memory).Update("status", model.MemoryStatusDeleting).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.LongTermMemoryJob{}).Where("memory_id = ? AND status IN ?", memory.ID, []string{model.MemoryJobQueued, model.MemoryJobPublished}).Update("status", model.MemoryJobCancelled).Error; err != nil {
				return err
			}
			tombstone := model.LongTermMemoryForgetTombstone{UserID: userID, KnowledgeBaseID: memory.KnowledgeBaseID, IdentityHash: memory.IdentityHash, SubjectHash: HashMemoryText(memory.Subject), ForgetBeforeChatLogID: maxChatLogID, CreatedAt: time.Now()}
			if err := tx.Create(&tombstone).Error; err != nil {
				return err
			}
			if err := createMemoryJob(tx, model.MemoryJobDeleteVector, userID, memory.KnowledgeBaseID, &memory.ID, nil, map[string]any{"memory_id": memory.ID}); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

// CompleteDelete 彻底清理已删除记忆的派生正文
func (r *LongTermMemoryRepository) CompleteDelete(id, userID uint64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("memory_id = ?", id).Delete(&model.LongTermMemoryEvidence{}).Error; err != nil {
			return err
		}
		if err := tx.Where("memory_id = ?", id).Delete(&model.LongTermMemoryVersion{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND user_id = ? AND status = ?", id, userID, model.MemoryStatusDeleting).Delete(&model.LongTermMemory{}).Error
	})
}

// createMemoryJob 在当前事务中创建无正文异步任务
func createMemoryJob(tx *gorm.DB, jobType string, userID uint64, kbID, memoryID, chatLogID *uint64, payload any) error {
	raw, _ := json.Marshal(payload)
	job := model.LongTermMemoryJob{ID: uuid.NewString(), Type: jobType, UserID: userID, KnowledgeBaseID: kbID, MemoryID: memoryID, ChatLogID: chatLogID, PayloadJSON: raw, Status: model.MemoryJobQueued, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	return tx.Create(&job).Error
}

// AddSignal 写入去重后的记忆归并信号
func (r *LongTermMemoryRepository) AddSignal(tx *gorm.DB, signal *model.MemoryConsolidationSignal) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(signal).Error
}

// MaxChatLogID 查询用户当前最大的聊天日志ID
func (r *LongTermMemoryRepository) MaxChatLogID(userID uint64) uint64 {
	var id uint64
	_ = r.db.Model(&model.ChatLog{}).Where("user_id = ?", userID).Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id
}

// FindJob 查询长期记忆异步任务
func (r *LongTermMemoryRepository) FindJob(id string) (*model.LongTermMemoryJob, error) {
	var job model.LongTermMemoryJob
	err := r.db.Where("id = ?", id).First(&job).Error
	return &job, err
}

// ClaimJob 抢占长期记忆任务处理租约
func (r *LongTermMemoryRepository) ClaimJob(id string, now, leaseUntil time.Time) (bool, error) {
	result := r.db.Model(&model.LongTermMemoryJob{}).Where("id = ?", id).Where("status IN ? OR (status = ? AND (lease_until IS NULL OR lease_until < ?))", []string{model.MemoryJobQueued, model.MemoryJobPublished, model.MemoryJobFailed}, model.MemoryJobProcessing, now).Updates(map[string]any{"status": model.MemoryJobProcessing, "lease_until": leaseUntil, "attempt": gorm.Expr("attempt + 1")})
	return result.RowsAffected == 1, result.Error
}

// CompleteJob 标记长期记忆任务已完成
func (r *LongTermMemoryRepository) CompleteJob(id string) error {
	return r.db.Model(&model.LongTermMemoryJob{}).Where("id = ?", id).Updates(map[string]any{"status": model.MemoryJobCompleted, "lease_until": nil, "last_error": ""}).Error
}

// FailJob 记录长期记忆任务失败状态
func (r *LongTermMemoryRepository) FailJob(id, message string) error {
	return r.db.Model(&model.LongTermMemoryJob{}).Where("id = ?", id).Updates(map[string]any{"status": model.MemoryJobFailed, "lease_until": nil, "last_error": message}).Error
}

// QueuedJobs 查询可发布或可恢复的长期记忆任务
func (r *LongTermMemoryRepository) QueuedJobs(limit int, now time.Time) ([]model.LongTermMemoryJob, error) {
	var jobs []model.LongTermMemoryJob
	err := r.db.Where("status IN ? OR (status IN ? AND lease_until < ?)", []string{model.MemoryJobQueued, model.MemoryJobFailed}, []string{model.MemoryJobPublished, model.MemoryJobProcessing}, now).Order("created_at").Limit(limit).Find(&jobs).Error
	return jobs, err
}

// MarkPublished 记录任务已发布及发布租约
func (r *LongTermMemoryRepository) MarkPublished(id string, leaseUntil time.Time) error {
	return r.db.Model(&model.LongTermMemoryJob{}).Where("id = ? AND status IN ?", id, []string{model.MemoryJobQueued, model.MemoryJobFailed}).Updates(map[string]any{"status": model.MemoryJobPublished, "lease_until": leaseUntil}).Error
}

// ScheduleSignalBatches 按会话锁定信号快照并创建归并任务
func (r *LongTermMemoryRepository) ScheduleSignalBatches(now time.Time, scanLimit, maxSignals, maxTokens int) ([]MemorySignalBatch, error) {
	if scanLimit <= 0 {
		scanLimit = 100
	}
	if maxSignals <= 0 {
		maxSignals = 10
	}
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	var pending []model.MemoryConsolidationSignal
	if err := r.db.Where("status = ?", model.MemorySignalPending).Order("created_at").Limit(scanLimit).Find(&pending).Error; err != nil {
		return nil, err
	}
	type group struct {
		signals []model.MemoryConsolidationSignal
		tokens  int
		due     bool
	}
	groups := map[string]*group{}
	order := make([]string, 0)
	for _, signal := range pending {
		key := fmt.Sprintf("%d/%d/%s", signal.UserID, signal.KnowledgeBaseID, signal.ConversationID)
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		g.signals = append(g.signals, signal)
		g.tokens += signal.EstimatedTokens
		g.due = g.due || !signal.EligibleAt.After(now)
	}
	var batches []MemorySignalBatch
	for _, key := range order {
		g := groups[key]
		if !g.due && len(g.signals) < maxSignals && g.tokens < maxTokens {
			continue
		}
		selected, tokens := make([]model.MemoryConsolidationSignal, 0, maxSignals), 0
		for _, signal := range g.signals {
			if len(selected) >= maxSignals || (len(selected) > 0 && tokens+signal.EstimatedTokens > maxTokens) {
				break
			}
			selected = append(selected, signal)
			tokens += signal.EstimatedTokens
		}
		if len(selected) == 0 {
			continue
		}
		var batch MemorySignalBatch
		err := r.db.Transaction(func(tx *gorm.DB) error {
			ids := make([]uint64, 0, len(selected))
			for _, signal := range selected {
				ids = append(ids, signal.ID)
			}
			taskID := uuid.NewString()
			result := tx.Model(&model.MemoryConsolidationSignal{}).Where("id IN ? AND status = ?", ids, model.MemorySignalPending).Updates(map[string]any{"status": model.MemorySignalQueued, "task_id": taskID})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != int64(len(ids)) {
				return ErrMemoryStateChanged
			}
			raw, _ := json.Marshal(map[string]any{"signal_ids": ids})
			kbID := selected[0].KnowledgeBaseID
			job := model.LongTermMemoryJob{ID: taskID, Type: model.MemoryJobConsolidate, UserID: selected[0].UserID, KnowledgeBaseID: &kbID, PayloadJSON: raw, Status: model.MemoryJobQueued, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&job).Error; err != nil {
				return err
			}
			batch = MemorySignalBatch{Job: job, SignalIDs: ids}
			return nil
		})
		if err != nil {
			if errors.Is(err, ErrMemoryStateChanged) {
				continue
			}
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

// SignalsByIDs 查询归属于用户的固定信号快照
func (r *LongTermMemoryRepository) SignalsByIDs(userID uint64, ids []uint64) ([]model.MemoryConsolidationSignal, error) {
	var signals []model.MemoryConsolidationSignal
	err := r.db.Where("id IN ? AND user_id = ? AND status IN ?", ids, userID, []string{model.MemorySignalQueued, model.MemorySignalProcessing}).Order("id").Find(&signals).Error
	return signals, err
}

// CompleteSignals 标记同一归并任务的信号已完成
func (r *LongTermMemoryRepository) CompleteSignals(taskID string) error {
	return r.db.Model(&model.MemoryConsolidationSignal{}).Where("task_id = ?", taskID).Updates(map[string]any{"status": model.MemorySignalCompleted}).Error
}

// MemoryForIndex 查询待索引的当前记忆
func (r *LongTermMemoryRepository) MemoryForIndex(id uint64) (*model.LongTermMemory, error) {
	var memory model.LongTermMemory
	err := r.db.Where("id = ?", id).First(&memory).Error
	return &memory, err
}

// IsMemoryIndexCurrent 判断索引写入后记忆版本是否仍然有效
func (r *LongTermMemoryRepository) IsMemoryIndexCurrent(id, version uint64) bool {
	var count int64
	_ = r.db.Model(&model.LongTermMemory{}).Where("id = ? AND version = ? AND status IN ?", id, version, []string{model.MemoryStatusActive, model.MemoryStatusCandidate}).Count(&count).Error
	return count == 1
}

// IsMemoryExpired 判断记忆是否仍处于过期状态
func (r *LongTermMemoryRepository) IsMemoryExpired(id uint64) bool {
	var count int64
	_ = r.db.Model(&model.LongTermMemory{}).Where("id = ? AND status = ?", id, model.MemoryStatusExpired).Count(&count).Error
	return count == 1
}

// MarkIndexed 提交指定版本的向量索引状态
func (r *LongTermMemoryRepository) MarkIndexed(id, version uint64, modelName, hash string) error {
	result := r.db.Model(&model.LongTermMemory{}).Where("id = ? AND version = ? AND status IN ?", id, version, []string{model.MemoryStatusActive, model.MemoryStatusCandidate}).Updates(map[string]any{"embedding_status": model.MemoryEmbeddingReady, "embedding_model": modelName, "embedding_hash": hash, "vector_id": fmt.Sprint(id)})
	return result.Error
}

// ExpireDue 标记到期记忆并创建向量清理任务
func (r *LongTermMemoryRepository) ExpireDue(now time.Time, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var memories []model.LongTermMemory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status IN ? AND expires_at IS NOT NULL AND expires_at <= ?", []string{model.MemoryStatusActive, model.MemoryStatusCandidate, model.MemoryStatusConflicted}, now).Limit(limit).Find(&memories).Error; err != nil {
			return err
		}
		for _, memory := range memories {
			if err := tx.Model(&memory).Update("status", model.MemoryStatusExpired).Error; err != nil {
				return err
			}
			if err := createMemoryJob(tx, model.MemoryJobExpire, memory.UserID, memory.KnowledgeBaseID, &memory.ID, nil, map[string]any{"memory_id": memory.ID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// MergeCandidate 合并候选证据并处理冲突和自动升级
func (r *LongTermMemoryRepository) MergeCandidate(memory *model.LongTermMemory, evidence model.LongTermMemoryEvidence, autoPromoteMin int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current model.LongTermMemory
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("identity_hash = ?", memory.IdentityHash).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var tombstoneCount int64
			if err := tx.Model(&model.LongTermMemoryForgetTombstone{}).Where("user_id = ? AND identity_hash = ? AND forget_before_chat_log_id >= ?", memory.UserID, memory.IdentityHash, evidence.ChatLogID).Count(&tombstoneCount).Error; err != nil {
				return err
			}
			if tombstoneCount > 0 {
				return nil
			}
			if err := tx.Create(memory).Error; err != nil {
				return err
			}
			evidence.MemoryID = memory.ID
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence).Error; err != nil {
				return err
			}
			memory.EvidenceCount = 1
			memory.ConversationCount = 1
			if err := tx.Model(memory).Updates(map[string]any{"evidence_count": 1, "conversation_count": 1}).Error; err != nil {
				return err
			}
			snapshot, _ := json.Marshal(memory)
			if err := tx.Create(&model.LongTermMemoryVersion{MemoryID: memory.ID, Version: memory.Version, SnapshotJSON: snapshot, ChangeType: "create", SourceChatLogID: &evidence.ChatLogID, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
			return createMemoryJob(tx, model.MemoryJobIndex, memory.UserID, memory.KnowledgeBaseID, &memory.ID, &evidence.ChatLogID, map[string]any{"version": memory.Version})
		}
		if err != nil {
			return err
		}
		if current.Status == model.MemoryStatusDeleting {
			return nil
		}
		evidence.MemoryID = current.ID
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		var evidenceCount, conversationCount int64
		if err := tx.Model(&model.LongTermMemoryEvidence{}).Where("memory_id = ?", current.ID).Count(&evidenceCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.LongTermMemoryEvidence{}).Where("memory_id = ?", current.ID).Distinct("conversation_id").Count(&conversationCount).Error; err != nil {
			return err
		}
		updates := map[string]any{"evidence_count": evidenceCount, "conversation_count": conversationCount}
		if normalizeMemoryValue(current.Value) != normalizeMemoryValue(memory.Value) {
			if current.Status == model.MemoryStatusActive || current.Status == model.MemoryStatusCandidate {
				updates["status"] = model.MemoryStatusConflicted
			}
			return tx.Model(&current).Updates(updates).Error
		}
		if autoPromoteMin <= 0 {
			autoPromoteMin = 2
		}
		promoted := current.Status == model.MemoryStatusCandidate && int(conversationCount) >= autoPromoteMin && canAutoPromoteMemoryType(current.Type)
		if promoted {
			now := time.Now()
			updates["status"] = model.MemoryStatusActive
			updates["last_confirmed_at"] = &now
			updates["version"] = current.Version + 1
			updates["embedding_status"] = model.MemoryEmbeddingPending
		}
		if err := tx.Model(&current).Updates(updates).Error; err != nil {
			return err
		}
		if promoted {
			current.Status = model.MemoryStatusActive
			current.Version++
			current.LastConfirmedAt = updates["last_confirmed_at"].(*time.Time)
			current.EvidenceCount = int(evidenceCount)
			current.ConversationCount = int(conversationCount)
			current.EmbeddingStatus = model.MemoryEmbeddingPending
			snapshot, _ := json.Marshal(current)
			if err := tx.Create(&model.LongTermMemoryVersion{MemoryID: current.ID, Version: current.Version, SnapshotJSON: snapshot, ChangeType: "auto_promote", SourceChatLogID: &evidence.ChatLogID, CreatedAt: time.Now()}).Error; err != nil {
				return err
			}
			return createMemoryJob(tx, model.MemoryJobIndex, current.UserID, current.KnowledgeBaseID, &current.ID, &evidence.ChatLogID, map[string]any{"version": current.Version})
		}
		return nil
	})
}

func normalizeMemoryValue(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

func canAutoPromoteMemoryType(memoryType string) bool {
	switch memoryType {
	case "role", "business_object", "project_context", "terminology":
		return true
	default:
		return false
	}
}

// SaveTurnWithExplicitMemory 原子保存聊天和显式记忆操作
func (r *LongTermMemoryRepository) SaveTurnWithExplicitMemory(conversation *model.Conversation, log *model.ChatLog, processingToken string, expiresAt time.Time, operation model.PendingMemoryOperation, cfg config.LongTermMemoryConfig) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if conversation != nil {
			if err := tx.Create(conversation).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(log).Error; err != nil {
			return err
		}
		if conversation == nil && processingToken != "" {
			result := tx.Model(&model.Conversation{}).Where("id = ? AND user_id = ? AND knowledge_base_id = ? AND processing_token = ?", log.ConversationID, log.UserID, log.KnowledgeBaseID, processingToken).Updates(map[string]any{"unsummarized_tokens": gorm.Expr("unsummarized_tokens + ?", log.ConversationTokens), "last_message_at": log.CreatedAt, "expires_at": expiresAt, "processing_token": "", "processing_lease_until": nil})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrConversationProcessingLeaseLost
			}
		}
		kbID := log.KnowledgeBaseID
		identity := explicitMemoryIdentity(log.UserID, operation.Scope, kbID, operation.Type, operation.Subject, operation.Attribute)
		if operation.Kind == "forget" {
			var memories []model.LongTermMemory
			query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND status <> ?", log.UserID, model.MemoryStatusDeleting)
			if operation.Scope == model.MemoryScopeUserGlobal {
				query = query.Where("scope = ? AND knowledge_base_id IS NULL", model.MemoryScopeUserGlobal)
			} else {
				query = query.Where("scope = ? AND knowledge_base_id = ?", model.MemoryScopeKnowledgeBase, log.KnowledgeBaseID)
			}
			if operation.Subject != "" {
				query = query.Where("LOWER(subject) = LOWER(?)", operation.Subject)
			} else {
				query = query.Where("identity_hash = ?", identity)
			}
			if err := query.Find(&memories).Error; err != nil {
				return err
			}
			for _, memory := range memories {
				if err := tx.Model(&memory).Update("status", model.MemoryStatusDeleting).Error; err != nil {
					return err
				}
				tombstone := model.LongTermMemoryForgetTombstone{UserID: log.UserID, KnowledgeBaseID: memory.KnowledgeBaseID, IdentityHash: memory.IdentityHash, SubjectHash: HashMemoryText(memory.Subject), ForgetBeforeChatLogID: log.ID, CreatedAt: log.CreatedAt}
				if err := tx.Create(&tombstone).Error; err != nil {
					return err
				}
				if err := createMemoryJob(tx, model.MemoryJobDeleteVector, log.UserID, memory.KnowledgeBaseID, &memory.ID, &log.ID, map[string]any{"memory_id": memory.ID}); err != nil {
					return err
				}
			}
			return nil
		}
		var current model.LongTermMemory
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("identity_hash = ?", identity).First(&current).Error
		now := log.CreatedAt
		ttl := explicitMemoryTTL(cfg, operation.Type)
		expires := now.Add(time.Duration(ttl) * time.Hour)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			memory := model.LongTermMemory{UserID: log.UserID, KnowledgeBaseID: &kbID, Scope: operation.Scope, Type: operation.Type, MemoryKey: strings.ToLower(strings.TrimSpace(operation.Subject + "." + operation.Attribute)), IdentityHash: identity, Subject: operation.Subject, Attribute: operation.Attribute, Value: operation.Value, Content: operation.Content, Status: model.MemoryStatusActive, Durability: operation.Durability, Confidence: 1, Importance: 1, EvidenceCount: 1, ConversationCount: 1, Version: 1, EmbeddingStatus: model.MemoryEmbeddingPending, FirstObservedAt: now, LastConfirmedAt: &now, ExpiresAt: &expires, CreatedAt: now, UpdatedAt: now}
			if operation.Scope == model.MemoryScopeUserGlobal {
				memory.KnowledgeBaseID = nil
			}
			if err := tx.Create(&memory).Error; err != nil {
				return err
			}
			return r.createExplicitEvidenceAndVersion(tx, &memory, log, operation, "create")
		}
		if err != nil {
			return err
		}
		current.Value = operation.Value
		current.Content = operation.Content
		current.Status = model.MemoryStatusActive
		current.Durability = operation.Durability
		current.LastConfirmedAt = &now
		current.ExpiresAt = &expires
		current.Version++
		current.EmbeddingStatus = model.MemoryEmbeddingPending
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		return r.createExplicitEvidenceAndVersion(tx, &current, log, operation, "correct")
	})
}

func (r *LongTermMemoryRepository) createExplicitEvidenceAndVersion(tx *gorm.DB, memory *model.LongTermMemory, log *model.ChatLog, operation model.PendingMemoryOperation, changeType string) error {
	evidence := model.LongTermMemoryEvidence{MemoryID: memory.ID, UserID: log.UserID, ConversationID: log.ConversationID, ChatLogID: log.ID, EvidenceHash: HashMemoryText(operation.EvidenceExcerpt), EvidenceKind: "explicit_directive", Explicit: true, CreatedAt: log.CreatedAt}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence).Error; err != nil {
		return err
	}
	var evidenceCount, conversationCount int64
	if err := tx.Model(&model.LongTermMemoryEvidence{}).Where("memory_id = ?", memory.ID).Count(&evidenceCount).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.LongTermMemoryEvidence{}).Where("memory_id = ?", memory.ID).Distinct("conversation_id").Count(&conversationCount).Error; err != nil {
		return err
	}
	memory.EvidenceCount = int(evidenceCount)
	memory.ConversationCount = int(conversationCount)
	if err := tx.Model(memory).Updates(map[string]any{"evidence_count": evidenceCount, "conversation_count": conversationCount}).Error; err != nil {
		return err
	}
	snapshot, _ := json.Marshal(memory)
	if err := tx.Create(&model.LongTermMemoryVersion{MemoryID: memory.ID, Version: memory.Version, SnapshotJSON: snapshot, ChangeType: changeType, SourceChatLogID: &log.ID, CreatedAt: log.CreatedAt}).Error; err != nil {
		return err
	}
	return createMemoryJob(tx, model.MemoryJobIndex, log.UserID, memory.KnowledgeBaseID, &memory.ID, &log.ID, map[string]any{"version": memory.Version})
}

func explicitMemoryIdentity(userID uint64, scope string, kbID uint64, memoryType, subject, attribute string) string {
	if scope == "" {
		scope = model.MemoryScopeKnowledgeBase
	}
	if scope == model.MemoryScopeUserGlobal {
		kbID = 0
	}
	raw := fmt.Sprintf("%d\x00%s\x00%d\x00%s\x00%s\x00%s", userID, scope, kbID, strings.ToLower(strings.TrimSpace(memoryType)), strings.ToLower(strings.TrimSpace(subject)), strings.ToLower(strings.TrimSpace(attribute)))
	return HashMemoryText(raw)
}

func explicitMemoryTTL(cfg config.LongTermMemoryConfig, memoryType string) int {
	values := map[string]int{"preference": cfg.PreferenceTTLHours, "role": cfg.RoleTTLHours, "terminology": cfg.TerminologyTTLHours, "business_object": cfg.BusinessObjectTTLHours, "project_context": cfg.ProjectContextTTLHours}
	if ttl := values[memoryType]; ttl > 0 {
		return ttl
	}
	return 720
}
