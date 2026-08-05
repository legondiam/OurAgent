package service

import (
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"

	"gorm.io/gorm"
)

type MemoryLifecycleService struct {
	repo *repository.LongTermMemoryRepository
	kbs  *repository.KnowledgeBaseRepository
	cfg  config.LongTermMemoryConfig
}

type UpdateMemoryInput struct {
	Content         *string
	Value           *string
	Scope           *string
	KnowledgeBaseID *uint64
	Durability      *string
	ExpiresAt       *time.Time
}

// NewMemoryLifecycleService 创建长期记忆生命周期服务
func NewMemoryLifecycleService(repo *repository.LongTermMemoryRepository, kbs *repository.KnowledgeBaseRepository, cfg config.LongTermMemoryConfig) *MemoryLifecycleService {
	return &MemoryLifecycleService{repo: repo, kbs: kbs, cfg: cfg}
}

// List 查询用户可管理的长期记忆
func (s *MemoryLifecycleService) List(filter repository.MemoryListFilter) ([]model.LongTermMemory, int64, error) {
	if filter.KnowledgeBaseID != nil {
		if err := s.authorizeKB(filter.UserID, *filter.KnowledgeBaseID); err != nil {
			return nil, 0, err
		}
	}
	return s.repo.List(filter)
}

// Confirm 确认用户拥有的候选记忆
func (s *MemoryLifecycleService) Confirm(userID, id uint64) error {
	memory, err := s.repo.FindOwned(id, userID)
	if err != nil {
		return err
	}
	if err := s.authorizeMemoryKB(userID, memory); err != nil {
		return err
	}
	if !validStoredMemoryScope(memory.Type, memory.Scope, memory.KnowledgeBaseID) {
		return ErrInvalidMemoryScope
	}
	expires := time.Now().Add(time.Duration(memoryTTLHours(s.cfg, memory.Type, false)) * time.Hour)
	return s.repo.Confirm(id, userID, &expires)
}

// Update 修改长期记忆并重新生成索引版本
func (s *MemoryLifecycleService) Update(userID, id uint64, input UpdateMemoryInput) (*model.LongTermMemory, error) {
	memory, err := s.repo.FindOwned(id, userID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeMemoryKB(userID, memory); err != nil {
		return nil, err
	}
	if input.Content != nil {
		memory.Content = strings.TrimSpace(*input.Content)
	}
	if input.Value != nil {
		memory.Value = strings.TrimSpace(*input.Value)
	}
	if input.Durability != nil {
		memory.Durability = strings.TrimSpace(*input.Durability)
	}
	if input.ExpiresAt != nil {
		memory.ExpiresAt = input.ExpiresAt
	}
	if input.Scope != nil {
		if *input.Scope != model.MemoryScopeKnowledgeBase && *input.Scope != model.MemoryScopeUserGlobal {
			return nil, ErrInvalidMemoryScope
		}
		memory.Scope = *input.Scope
	}
	if input.KnowledgeBaseID != nil {
		if err := s.authorizeKB(userID, *input.KnowledgeBaseID); err != nil {
			return nil, err
		}
		memory.KnowledgeBaseID = input.KnowledgeBaseID
	}
	if memory.Scope == model.MemoryScopeUserGlobal {
		memory.KnowledgeBaseID = nil
	}
	if !validStoredMemoryScope(memory.Type, memory.Scope, memory.KnowledgeBaseID) {
		return nil, ErrInvalidMemoryScope
	}
	kbID := uint64(0)
	if memory.KnowledgeBaseID != nil {
		kbID = *memory.KnowledgeBaseID
	}
	memory.IdentityHash = memoryIdentityHash(userID, memory.Scope, kbID, memory.Type, memory.Subject, memory.Attribute)
	if memory.Content == "" || memory.Value == "" || containsSensitiveMemory(memory.Content+memory.Value) || containsEnterpriseMemoryFact(memory.Content+memory.Value) {
		return nil, ErrInvalidMemoryContent
	}
	if err := s.repo.UpdateOwned(memory, userID); err != nil {
		return nil, err
	}
	return memory, nil
}

// Delete 启动单条长期记忆的两阶段删除
func (s *MemoryLifecycleService) Delete(userID, id uint64) error {
	memory, err := s.repo.FindOwned(id, userID)
	if err != nil {
		return err
	}
	if err := s.authorizeMemoryKB(userID, memory); err != nil {
		return err
	}
	return s.repo.MarkDeleting(id, userID, s.repo.MaxChatLogID(userID))
}

// DeleteByScope 按作用域启动批量两阶段删除
func (s *MemoryLifecycleService) DeleteByScope(userID uint64, scope string, knowledgeBaseID *uint64) (int, error) {
	if scope != model.MemoryScopeKnowledgeBase && scope != model.MemoryScopeUserGlobal {
		return 0, ErrInvalidMemoryScope
	}
	if scope == model.MemoryScopeKnowledgeBase {
		if knowledgeBaseID == nil {
			return 0, ErrInvalidMemoryScope
		}
		if err := s.authorizeKB(userID, *knowledgeBaseID); err != nil {
			return 0, err
		}
	} else if knowledgeBaseID != nil {
		return 0, ErrInvalidMemoryScope
	}
	return s.repo.MarkDeletingByScope(userID, scope, knowledgeBaseID, s.repo.MaxChatLogID(userID))
}

// DeleteKnowledgeBaseMemories 清理知识库作用域内的长期记忆
func (s *MemoryLifecycleService) DeleteKnowledgeBaseMemories(userID, knowledgeBaseID uint64) error {
	_, err := s.repo.MarkDeletingByScope(userID, model.MemoryScopeKnowledgeBase, &knowledgeBaseID, s.repo.MaxChatLogID(userID))
	return err
}

func (s *MemoryLifecycleService) authorizeMemoryKB(userID uint64, memory *model.LongTermMemory) error {
	if memory.KnowledgeBaseID == nil {
		return nil
	}
	return s.authorizeKB(userID, *memory.KnowledgeBaseID)
}

func (s *MemoryLifecycleService) authorizeKB(userID, kbID uint64) error {
	exists, err := s.kbs.ExistsByIDAndUserID(kbID, userID)
	if err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func validMemoryTypeScope(memoryType, scope string) bool {
	if memoryType == "preference" {
		return scope == model.MemoryScopeUserGlobal
	}
	switch memoryType {
	case "role", "business_object", "project_context", "terminology", "instruction":
		return scope == model.MemoryScopeKnowledgeBase
	default:
		return false
	}
}

func validStoredMemoryScope(memoryType, scope string, knowledgeBaseID *uint64) bool {
	if !validMemoryTypeScope(memoryType, scope) {
		return false
	}
	if scope == model.MemoryScopeUserGlobal {
		return knowledgeBaseID == nil
	}
	return knowledgeBaseID != nil
}

var (
	ErrInvalidMemoryScope   = gorm.ErrInvalidValue
	ErrInvalidMemoryContent = gorm.ErrInvalidData
)
