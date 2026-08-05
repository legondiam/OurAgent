package model

import (
	"time"

	"gorm.io/datatypes"
)

const (
	MemoryScopeKnowledgeBase = "knowledge_base"
	MemoryScopeUserGlobal    = "user_global"

	MemoryStatusCandidate  = "candidate"
	MemoryStatusActive     = "active"
	MemoryStatusConflicted = "conflicted"
	MemoryStatusExpired    = "expired"
	MemoryStatusDeleting   = "deleting"

	MemoryEmbeddingPending = "pending"
	MemoryEmbeddingReady   = "ready"
	MemoryEmbeddingFailed  = "failed"

	MemorySignalPending    = "pending"
	MemorySignalQueued     = "queued"
	MemorySignalProcessing = "processing"
	MemorySignalCompleted  = "completed"
	MemorySignalCancelled  = "cancelled"

	MemoryJobQueued     = "queued"
	MemoryJobPublished  = "published"
	MemoryJobProcessing = "processing"
	MemoryJobCompleted  = "completed"
	MemoryJobFailed     = "failed"
	MemoryJobCancelled  = "cancelled"

	MemoryJobConsolidate  = "consolidate"
	MemoryJobIndex        = "index"
	MemoryJobDeleteVector = "delete_vector"
	MemoryJobExpire       = "expire"
)

type LongTermMemory struct {
	ID                uint64     `gorm:"primaryKey" json:"id"`
	UserID            uint64     `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID   *uint64    `gorm:"index" json:"knowledge_base_id,omitempty"`
	Scope             string     `gorm:"size:32;index;not null" json:"scope"`
	Type              string     `gorm:"size:32;index;not null" json:"type"`
	MemoryKey         string     `gorm:"size:255;not null" json:"memory_key"`
	IdentityHash      string     `gorm:"size:64;uniqueIndex;not null" json:"identity_hash"`
	Subject           string     `gorm:"size:255;index;not null" json:"subject"`
	Attribute         string     `gorm:"size:128;not null" json:"attribute"`
	Value             string     `gorm:"type:text;not null" json:"value"`
	Content           string     `gorm:"type:text;not null" json:"content"`
	Status            string     `gorm:"size:32;index;not null" json:"status"`
	Durability        string     `gorm:"size:32;not null" json:"durability"`
	Confidence        float64    `json:"confidence"`
	Importance        float64    `json:"importance"`
	EvidenceCount     int        `gorm:"not null;default:0" json:"evidence_count"`
	ConversationCount int        `gorm:"not null;default:0" json:"conversation_count"`
	Version           uint64     `gorm:"not null;default:1" json:"version"`
	EmbeddingStatus   string     `gorm:"size:32;index;not null" json:"embedding_status"`
	EmbeddingModel    string     `gorm:"size:100" json:"embedding_model"`
	EmbeddingHash     string     `gorm:"size:64" json:"embedding_hash"`
	VectorID          string     `gorm:"size:128;index" json:"vector_id"`
	FirstObservedAt   time.Time  `json:"first_observed_at"`
	LastConfirmedAt   *time.Time `gorm:"index" json:"last_confirmed_at"`
	LastUsedAt        *time.Time `json:"last_used_at"`
	ExpiresAt         *time.Time `gorm:"index" json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type LongTermMemoryVersion struct {
	ID              uint64         `gorm:"primaryKey" json:"id"`
	MemoryID        uint64         `gorm:"uniqueIndex:idx_memory_version;not null" json:"memory_id"`
	Version         uint64         `gorm:"uniqueIndex:idx_memory_version;not null" json:"version"`
	SnapshotJSON    datatypes.JSON `gorm:"type:json;not null" json:"snapshot_json"`
	ChangeType      string         `gorm:"size:32;index;not null" json:"change_type"`
	SourceChatLogID *uint64        `gorm:"index" json:"source_chat_log_id,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type LongTermMemoryEvidence struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	MemoryID       uint64    `gorm:"uniqueIndex:idx_memory_evidence;not null" json:"memory_id"`
	UserID         uint64    `gorm:"index;not null" json:"user_id"`
	ConversationID string    `gorm:"size:64;index;not null" json:"conversation_id"`
	ChatLogID      uint64    `gorm:"uniqueIndex:idx_memory_evidence;index;not null" json:"chat_log_id"`
	EvidenceHash   string    `gorm:"size:64;uniqueIndex:idx_memory_evidence;not null" json:"evidence_hash"`
	EvidenceKind   string    `gorm:"size:32;not null" json:"evidence_kind"`
	Explicit       bool      `gorm:"not null" json:"explicit"`
	CreatedAt      time.Time `json:"created_at"`
}

type LongTermMemoryJob struct {
	ID              string         `gorm:"size:64;primaryKey" json:"id"`
	Type            string         `gorm:"size:32;index;not null" json:"type"`
	UserID          uint64         `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID *uint64        `gorm:"index" json:"knowledge_base_id,omitempty"`
	MemoryID        *uint64        `gorm:"index" json:"memory_id,omitempty"`
	ChatLogID       *uint64        `gorm:"index" json:"chat_log_id,omitempty"`
	PayloadJSON     datatypes.JSON `gorm:"type:json" json:"payload_json"`
	Status          string         `gorm:"size:32;index;not null" json:"status"`
	Attempt         int            `gorm:"not null;default:0" json:"attempt"`
	LeaseUntil      *time.Time     `gorm:"index" json:"lease_until"`
	LastError       string         `gorm:"type:text" json:"last_error"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type LongTermMemoryForgetTombstone struct {
	ID                    uint64    `gorm:"primaryKey" json:"id"`
	UserID                uint64    `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID       *uint64   `gorm:"index" json:"knowledge_base_id,omitempty"`
	IdentityHash          string    `gorm:"size:64;index" json:"identity_hash"`
	SubjectHash           string    `gorm:"size:64;index" json:"subject_hash"`
	ForgetBeforeChatLogID uint64    `gorm:"not null" json:"forget_before_chat_log_id"`
	CreatedAt             time.Time `json:"created_at"`
}

type MemoryConsolidationSignal struct {
	ID              uint64    `gorm:"primaryKey" json:"id"`
	UserID          uint64    `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID uint64    `gorm:"index;not null" json:"knowledge_base_id"`
	ConversationID  string    `gorm:"size:64;index;not null" json:"conversation_id"`
	ChatLogID       uint64    `gorm:"uniqueIndex;not null" json:"chat_log_id"`
	SignalType      string    `gorm:"size:32;index;not null" json:"signal_type"`
	SignalSource    string    `gorm:"size:32;not null" json:"signal_source"`
	EstimatedTokens int       `gorm:"not null;default:0" json:"estimated_tokens"`
	Status          string    `gorm:"size:32;index;not null" json:"status"`
	TaskID          string    `gorm:"size:64;index" json:"task_id"`
	EligibleAt      time.Time `gorm:"index;not null" json:"eligible_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type PendingMemoryOperation struct {
	Kind            string
	Type            string
	Scope           string
	Subject         string
	Attribute       string
	Value           string
	Content         string
	Durability      string
	EvidenceExcerpt string
	Explicit        bool
}
