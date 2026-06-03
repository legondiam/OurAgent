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
	Queries         []RewrittenQuery
	TopK            int
	BM25TopK        int
	HybridEnabled   bool
	BM25Enabled     bool
	RRFK            int
	Trace           *RetrievalTrace
}

type RetrievedChunk struct {
	Chunk          model.DocumentParentChunk
	MatchedChunk   model.DocumentChildChunk
	Document       model.Document
	Score          float64
	MatchedQueries []string
	RecallSources  []string
	VectorScore    float64
	BM25Score      float64
	RRFScore       float64
}

type Request struct {
	UserID                      uint64
	KnowledgeBaseID             uint64
	Question                    string
	TopK                        int
	ScoreThreshold              float64
	MaxContextTokens            int
	StrictMode                  bool
	QueryRewrite                bool
	QueryRewriteMaxQueries      int
	QueryRewriteIncludeOriginal bool
	HybridEnabled               bool
	BM25Enabled                 bool
	BM25TopK                    int
	RRFK                        int
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
	ParentChunkID  uint64  `json:"parent_chunk_id"`
	ChunkID        uint64  `json:"chunk_id"`
	ChunkIndex     int     `json:"chunk_index"`
	Score          float64 `json:"score"`
	ContentPreview string  `json:"content_preview"`
}

type RetrievalTrace struct {
	Query             string       `json:"query"`
	TopK              int          `json:"top_k"`
	ScoreThreshold    float64      `json:"score_threshold"`
	MaxContextTokens  int          `json:"max_context_tokens"`
	StrictMode        bool         `json:"strict_mode"`
	RewriteEnabled    bool         `json:"rewrite_enabled"`
	RewrittenQueries  []TraceQuery `json:"rewritten_queries"`
	RewriteError      string       `json:"rewrite_error,omitempty"`
	HybridEnabled     bool         `json:"hybrid_enabled"`
	BM25Enabled       bool         `json:"bm25_enabled"`
	RRFK              int          `json:"rrf_k"`
	BM25Error         string       `json:"bm25_error,omitempty"`
	Hits              []TraceHit   `json:"hits"`
	UsedChunkCount    int          `json:"used_chunk_count"`
	FilteredCount     int          `json:"filtered_count"`
	ContextTokenCount int          `json:"context_token_count"`
	RejectReason      string       `json:"reject_reason,omitempty"`
}

type TraceHit struct {
	ChunkID        uint64   `json:"chunk_id"`
	DocumentID     uint64   `json:"document_id"`
	DocumentName   string   `json:"document_name"`
	SectionPath    string   `json:"section_path"`
	ParentChunkID  uint64   `json:"parent_chunk_id"`
	ChunkIndex     int      `json:"chunk_index"`
	Score          float64  `json:"score"`
	MatchedQueries []string `json:"matched_queries,omitempty"`
	RecallSources  []string `json:"recall_sources,omitempty"`
	VectorScore    float64  `json:"vector_score,omitempty"`
	BM25Score      float64  `json:"bm25_score,omitempty"`
	RRFScore       float64  `json:"rrf_score,omitempty"`
	Used           bool     `json:"used"`
	Reason         string   `json:"reason,omitempty"`
}

type TraceQuery struct {
	Query  string `json:"query"`
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

func NewTrace(req Request, rejectReason string) RetrievalTrace {
	return RetrievalTrace{
		Query:            req.Question,
		TopK:             req.TopK,
		ScoreThreshold:   req.ScoreThreshold,
		MaxContextTokens: req.MaxContextTokens,
		StrictMode:       req.StrictMode,
		RewriteEnabled:   req.QueryRewrite,
		HybridEnabled:    req.HybridEnabled,
		BM25Enabled:      req.BM25Enabled,
		RRFK:             req.RRFK,
		Hits:             []TraceHit{},
		RewrittenQueries: []TraceQuery{},
		RejectReason:     rejectReason,
	}
}
