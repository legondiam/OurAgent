package rag

import (
	"context"

	"OurAgent/internal/model"

	"github.com/cloudwego/eino/schema"
)

const FallbackAnswer = "根据当前知识库内容无法确认。"

type Retriever interface {
	Retrieve(ctx context.Context, req RetrieveRequest) ([]RetrievedChunk, error)
}

type RetrieveRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Query           string
	TopK            int
}

type RetrievedChunk struct {
	Chunk    model.DocumentChunk
	Document model.Document
	Score    float64
}

type Request struct {
	UserID           uint64
	KnowledgeBaseID  uint64
	Question         string
	TopK             int
	ScoreThreshold   float64
	MaxContextTokens int
	StrictMode       bool
}

type PreparedChat struct {
	Request          Request
	Messages         []*schema.Message
	Answer           string
	Sources          []Source
	Trace            RetrievalTrace
	PromptPreview    string
	ContextText      string
	PromptTokens     int
	CompletionTokens int
}

type StreamChunk struct {
	Content  string
	Prepared *PreparedChat
}

type Source struct {
	DocumentID     uint64  `json:"document_id"`
	DocumentName   string  `json:"document_name"`
	SectionPath    string  `json:"section_path"`
	ChunkID        uint64  `json:"chunk_id"`
	ChunkIndex     int     `json:"chunk_index"`
	Score          float64 `json:"score"`
	ContentPreview string  `json:"content_preview"`
}

type RetrievalTrace struct {
	Query             string     `json:"query"`
	TopK              int        `json:"top_k"`
	ScoreThreshold    float64    `json:"score_threshold"`
	MaxContextTokens  int        `json:"max_context_tokens"`
	StrictMode        bool       `json:"strict_mode"`
	Hits              []TraceHit `json:"hits"`
	UsedChunkCount    int        `json:"used_chunk_count"`
	FilteredCount     int        `json:"filtered_count"`
	ContextTokenCount int        `json:"context_token_count"`
	RejectReason      string     `json:"reject_reason,omitempty"`
}

type TraceHit struct {
	ChunkID      uint64  `json:"chunk_id"`
	DocumentID   uint64  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	SectionPath  string  `json:"section_path"`
	ChunkIndex   int     `json:"chunk_index"`
	Score        float64 `json:"score"`
	Used         bool    `json:"used"`
	Reason       string  `json:"reason,omitempty"`
}

func NewTrace(req Request, rejectReason string) RetrievalTrace {
	return RetrievalTrace{
		Query:            req.Question,
		TopK:             req.TopK,
		ScoreThreshold:   req.ScoreThreshold,
		MaxContextTokens: req.MaxContextTokens,
		StrictMode:       req.StrictMode,
		Hits:             []TraceHit{},
		RejectReason:     rejectReason,
	}
}
