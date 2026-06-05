package tasks

import "time"

const (
	TypeDocumentIndex         = "document.index"
	TypeDocumentDeleteCleanup = "document.delete.cleanup"
	TypeSourceSync            = "source.sync"
)

type DocumentIndexMessage struct {
	EventID         string    `json:"event_id"`
	Type            string    `json:"type"`
	DocumentID      uint64    `json:"document_id"`
	UserID          uint64    `json:"user_id"`
	KnowledgeBaseID uint64    `json:"knowledge_base_id"`
	Attempt         int       `json:"attempt"`
	CreatedAt       time.Time `json:"created_at"`
}

type DocumentDeleteCleanupMessage struct {
	EventID         string    `json:"event_id"`
	Type            string    `json:"type"`
	DocumentID      uint64    `json:"document_id"`
	UserID          uint64    `json:"user_id"`
	KnowledgeBaseID uint64    `json:"knowledge_base_id"`
	ObjectKey       string    `json:"object_key"`
	Attempt         int       `json:"attempt"`
	CreatedAt       time.Time `json:"created_at"`
}

type SourceSyncMessage struct {
	EventID         string    `json:"event_id"`
	Type            string    `json:"type"`
	SourceID        uint64    `json:"source_id"`
	UserID          uint64    `json:"user_id"`
	KnowledgeBaseID uint64    `json:"knowledge_base_id"`
	Attempt         int       `json:"attempt"`
	CreatedAt       time.Time `json:"created_at"`
}
