package model

import (
	"time"

	"gorm.io/datatypes"
)

type User struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"size:100;uniqueIndex;not null" json:"username"`
	Email        string    `gorm:"size:255" json:"email"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type KnowledgeBase struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	UserID      uint64    `gorm:"index;not null" json:"user_id"`
	Name        string    `gorm:"size:255;not null" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Document struct {
	ID              uint64    `gorm:"primaryKey" json:"id"`
	KnowledgeBaseID uint64    `gorm:"index;not null" json:"knowledge_base_id"`
	UserID          uint64    `gorm:"index;not null" json:"user_id"`
	Filename        string    `gorm:"size:255;not null" json:"filename"`
	FileType        string    `gorm:"size:32;not null" json:"file_type"`
	FilePath        string    `gorm:"size:1024" json:"file_path"`
	BucketName      string    `gorm:"size:128" json:"bucket_name"`
	ObjectKey       string    `gorm:"size:1024;index" json:"object_key"`
	FileSize        int64     `json:"file_size"`
	ContentType     string    `gorm:"size:128" json:"content_type"`
	Status          string    `gorm:"size:32;index;not null" json:"status"`
	ErrorMessage    string    `gorm:"type:text" json:"error_message"`
	ChunkCount      int       `json:"chunk_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type DocumentParentChunk struct {
	ID              uint64    `gorm:"primaryKey" json:"id"`
	DocumentID      uint64    `gorm:"index;not null" json:"document_id"`
	KnowledgeBaseID uint64    `gorm:"index;not null" json:"knowledge_base_id"`
	UserID          uint64    `gorm:"index;not null" json:"user_id"`
	ChunkIndex      int       `gorm:"not null" json:"chunk_index"`
	SectionPath     string    `gorm:"size:1024" json:"section_path"`
	Content         string    `gorm:"type:longtext;not null" json:"content"`
	TokenCount      int       `json:"token_count"`
	CreatedAt       time.Time `json:"created_at"`
}

type DocumentChildChunk struct {
	ID              uint64    `gorm:"primaryKey" json:"id"`
	ParentChunkID   uint64    `gorm:"index;not null" json:"parent_chunk_id"`
	DocumentID      uint64    `gorm:"index;not null" json:"document_id"`
	KnowledgeBaseID uint64    `gorm:"index;not null" json:"knowledge_base_id"`
	UserID          uint64    `gorm:"index;not null" json:"user_id"`
	ChunkIndex      int       `gorm:"not null" json:"chunk_index"`
	SectionPath     string    `gorm:"size:1024" json:"section_path"`
	Content         string    `gorm:"type:longtext;not null" json:"content"`
	TokenCount      int       `json:"token_count"`
	VectorID        string    `gorm:"size:128;index" json:"vector_id"`
	CreatedAt       time.Time `json:"created_at"`
}

type ChatLog struct {
	ID               uint64         `gorm:"primaryKey" json:"id"`
	KnowledgeBaseID  uint64         `gorm:"index;not null" json:"knowledge_base_id"`
	UserID           uint64         `gorm:"index;not null" json:"user_id"`
	Question         string         `gorm:"type:text;not null" json:"question"`
	Answer           string         `gorm:"type:longtext;not null" json:"answer"`
	RetrievedChunks  datatypes.JSON `gorm:"type:json" json:"retrieved_chunks"`
	RetrievalTrace   datatypes.JSON `gorm:"type:json" json:"retrieval_trace"`
	PromptPreview    string         `gorm:"type:text" json:"prompt_preview"`
	ModelName        string         `gorm:"size:100" json:"model_name"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	ScoreThreshold   float64        `json:"score_threshold"`
	TopK             int            `json:"top_k"`
	MaxContextTokens int            `json:"max_context_tokens"`
	StrictMode       bool           `json:"strict_mode"`
	LatencyMS        int64          `json:"latency_ms"`
	CreatedAt        time.Time      `json:"created_at"`
}

type ChatFeedback struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	ChatLogID uint64    `gorm:"uniqueIndex:idx_chat_feedback_user_log;not null" json:"chat_log_id"`
	UserID    uint64    `gorm:"uniqueIndex:idx_chat_feedback_user_log;not null" json:"user_id"`
	Rating    string    `gorm:"size:32;not null" json:"rating"`
	Reason    string    `gorm:"type:text" json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
