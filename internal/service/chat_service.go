package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/document"
	"OurAgent/internal/model"
	"OurAgent/internal/rag"
	"OurAgent/internal/repository"
	"OurAgent/internal/websearch"

	einomodel "github.com/cloudwego/eino/components/model"
	pkgerrors "github.com/pkg/errors"
	"gorm.io/datatypes"
)

type ChatService struct {
	kbs   *repository.KnowledgeBaseRepository
	docs  *repository.DocumentRepository
	logs  *repository.ChatLogRepository
	chain *rag.RAGChain
	web   websearch.Answerer
	cfg   *config.Config
}

type ChatRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	ConversationID  string
	Question        string
	WebSearch       bool
}

type KnowledgeSearchOptions struct {
	QueryRewrite *bool
	Hybrid       *bool
	Rerank       *bool
	TopK         int
	Query        string
}

type ChatResponse struct {
	Answer    string       `json:"answer"`
	Sources   []rag.Source `json:"sources"`
	ChatLogID uint64       `json:"chat_log_id"`
}

type StreamEvent struct {
	Type      string
	Content   string
	Sources   []rag.Source
	ChatLogID uint64
	Err       error
}

type FeedbackInput struct {
	UserID    uint64
	ChatLogID uint64
	Rating    string
	Reason    string
}

func NewChatService(ctx context.Context, kbs *repository.KnowledgeBaseRepository, docs *repository.DocumentRepository, logs *repository.ChatLogRepository, retriever rag.Retriever, rewriter rag.QueryRewriter, reranker rag.Reranker, chat einomodel.BaseChatModel, web websearch.Answerer, cfg *config.Config) (*ChatService, error) {
	ragChain, err := rag.NewRAGChain(ctx, retriever, rewriter, reranker, chat, cfg.LLM.ChatModel, cfg.Rerank.Model)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "初始化Eino RAG Chain失败")
	}
	return &ChatService{kbs: kbs, docs: docs, logs: logs, chain: ragChain, web: web, cfg: cfg}, nil
}

// Chat执行知识库检索并生成回答
func (s *ChatService) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()

	// 校验业务权限并补齐检索参数
	resolved, prepared, err := s.validate(ctx, req)
	if err != nil {
		return nil, err
	}
	// 没有提前拒答时进入RAGChain
	if prepared == nil {
		prepared, err = s.chain.Invoke(ctx, resolved)
		if err != nil {
			return nil, err
		}
	}
	prepared = s.applyWebFallback(ctx, prepared)

	// 保存问答日志并返回引用来源
	logID, err := s.saveLog(prepared, start)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "保存问答日志失败")
	}
	return &ChatResponse{Answer: prepared.Answer, Sources: prepared.Sources, ChatLogID: logID}, nil
}

// PrepareKnowledgeAnswer执行纯知识库RAG回答
func (s *ChatService) PrepareKnowledgeAnswer(ctx context.Context, req ChatRequest) (*rag.PreparedChat, error) {
	return s.PrepareKnowledgeAnswerWithOptions(ctx, req, KnowledgeSearchOptions{})
}

// PrepareKnowledgeAnswerWithOptions按指定计划执行纯知识库RAG回答
func (s *ChatService) PrepareKnowledgeAnswerWithOptions(ctx context.Context, req ChatRequest, opts KnowledgeSearchOptions) (*rag.PreparedChat, error) {
	resolved, prepared, err := s.validate(ctx, req)
	if err != nil {
		return nil, err
	}
	if prepared != nil {
		return prepared, nil
	}
	applyKnowledgeSearchOptions(&resolved, opts)
	return s.chain.Invoke(ctx, resolved)
}

// Stream执行知识库检索并流式生成回答
func (s *ChatService) Stream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	start := time.Now()
	// 校验业务权限并补齐检索参数
	resolved, prepared, err := s.validate(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		// 验证阶段已经拒答时直接返回流式事件
		if prepared != nil && prepared.Answer != "" {
			prepared = s.applyWebFallback(ctx, prepared)
			logID, err := s.saveLog(prepared, start)
			if err != nil {
				out <- StreamEvent{Type: "error", Err: pkgerrors.WithMessage(err, "保存问答日志失败")}
				return
			}
			out <- StreamEvent{Type: "message", Content: prepared.Answer}
			out <- StreamEvent{Type: "sources", Sources: prepared.Sources}
			out <- StreamEvent{Type: "done", ChatLogID: logID}
			return
		}

		// 调用流式RAGChain并获取输出reader
		reader, err := s.chain.Stream(ctx, resolved)
		if err != nil {
			out <- StreamEvent{Type: "error", Err: err}
			return
		}
		defer reader.Close()
		var final *rag.PreparedChat
		var withheldFallback strings.Builder
		sentContent := false

		// 持续读取模型片段，最终结果用于落库
		for {
			chunk, err := reader.Recv()
			if stderrors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				out <- StreamEvent{Type: "error", Err: err}
				return
			}
			if chunk.Prepared != nil {
				final = chunk.Prepared
				continue
			}
			if chunk.Content != "" {
				if chunk.Content == rag.FallbackAnswer {
					withheldFallback.WriteString(chunk.Content)
					continue
				}
				if withheldFallback.Len() > 0 {
					out <- StreamEvent{Type: "message", Content: withheldFallback.String()}
					withheldFallback.Reset()
					sentContent = true
				}
				out <- StreamEvent{Type: "message", Content: chunk.Content}
				sentContent = true
			}
		}

		// 没有收到最终结果时兜底拒答
		if final == nil {
			final = &rag.PreparedChat{
				Request: resolved,
				Answer:  rag.FallbackAnswer,
				Sources: []rag.Source{},
				Trace:   rag.NewTrace(resolved, "流式回答没有返回最终结果"),
			}
		}
		final = s.applyWebFallback(ctx, final)
		if final.Trace.WebFallbackUsed {
			out <- StreamEvent{Type: "message", Content: final.Answer}
			sentContent = true
		} else if withheldFallback.Len() > 0 {
			out <- StreamEvent{Type: "message", Content: withheldFallback.String()}
			sentContent = true
		} else if !sentContent && final.Answer != "" {
			out <- StreamEvent{Type: "message", Content: final.Answer}
		}

		// 流式结束后保存日志并发送来源和完成事件
		logID, err := s.saveLog(final, start)
		if err != nil {
			out <- StreamEvent{Type: "error", Err: pkgerrors.WithMessage(err, "保存问答日志失败")}
			return
		}
		out <- StreamEvent{Type: "sources", Sources: final.Sources}
		out <- StreamEvent{Type: "done", ChatLogID: logID}
	}()
	return out, nil
}

// ListLogs查询用户问答日志
func (s *ChatService) ListLogs(userID uint64) ([]model.ChatLog, error) {
	logs, err := s.logs.ListByUserID(userID, 100)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询问答日志失败")
	}
	return logs, nil
}

// SubmitFeedback保存用户问答反馈
func (s *ChatService) SubmitFeedback(input FeedbackInput) (*model.ChatFeedback, error) {
	input.Rating = strings.TrimSpace(input.Rating)
	if input.Rating != "like" && input.Rating != "dislike" {
		return nil, pkgerrors.WithStack(ErrInvalidFeedback)
	}
	if _, err := s.logs.FindByIDAndUserID(input.ChatLogID, input.UserID); err != nil {
		return nil, pkgerrors.WithStack(ErrChatLogNotFound)
	}
	feedback := &model.ChatFeedback{
		ChatLogID: input.ChatLogID,
		UserID:    input.UserID,
		Rating:    input.Rating,
		Reason:    strings.TrimSpace(input.Reason),
	}
	if err := s.logs.UpsertFeedback(feedback); err != nil {
		return nil, pkgerrors.WithMessage(err, "保存问答反馈失败")
	}
	return feedback, nil
}

func (s *ChatService) validate(ctx context.Context, req ChatRequest) (rag.Request, *rag.PreparedChat, error) {
	resolved, err := s.authorizeRequest(ctx, req)
	if err != nil {
		return rag.Request{}, nil, err
	}

	completedDocs, err := s.docs.CountCompleted(resolved.UserID, resolved.KnowledgeBaseID)
	if err != nil {
		return rag.Request{}, nil, pkgerrors.WithMessage(err, "查询已完成索引文档失败")
	}
	if completedDocs == 0 {
		trace := rag.NewTrace(resolved, "知识库没有已完成索引的文档")
		return resolved, &rag.PreparedChat{Request: resolved, Answer: rag.FallbackAnswer, Sources: []rag.Source{}, Trace: trace}, nil
	}

	return resolved, nil, nil
}

func (s *ChatService) authorizeRequest(_ context.Context, req ChatRequest) (rag.Request, error) {
	resolved, err := s.resolveRequest(req)
	if err != nil {
		return rag.Request{}, err
	}

	exists, err := s.kbs.ExistsByIDAndUserID(resolved.KnowledgeBaseID, resolved.UserID)
	if err != nil {
		return rag.Request{}, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	if !exists {
		return rag.Request{}, pkgerrors.WithStack(ErrKnowledgeBaseNotFound)
	}
	return resolved, nil
}

// applyWebFallback在知识库拒答时尝试联网降级
func (s *ChatService) applyWebFallback(ctx context.Context, prepared *rag.PreparedChat) *rag.PreparedChat {
	if prepared == nil {
		return prepared
	}
	prepared.Trace.WebFallbackEnabled = s.cfg.Web.Enabled && prepared.Request.WebSearch
	if !s.shouldUseWebFallback(prepared) {
		return prepared
	}
	prepared.Trace.WebFallbackReason = prepared.Trace.RejectReason
	prepared.Trace.WebSearchProvider = s.cfg.Web.Provider
	prepared.Trace.WebSearchModel = s.cfg.Web.Model

	result, err := s.web.Answer(ctx, websearch.Request{
		UserID:          prepared.Request.UserID,
		KnowledgeBaseID: prepared.Request.KnowledgeBaseID,
		Question:        prepared.Request.Question,
	})
	if err != nil {
		prepared.Trace.WebSearchError = err.Error()
		return prepared
	}
	prepared.Answer = result.Answer
	prepared.Sources = webSourcesToRAGSources(result.Sources)
	prepared.Trace.WebFallbackUsed = true
	prepared.Trace.WebSearchResultCount = len(result.Sources)
	return prepared
}

// shouldUseWebFallback判断是否需要触发联网降级
func (s *ChatService) shouldUseWebFallback(prepared *rag.PreparedChat) bool {
	if s.web == nil || !s.cfg.Web.Enabled || !s.cfg.Web.FallbackOnly || !prepared.Request.WebSearch {
		return false
	}
	return prepared.Answer == rag.FallbackAnswer && prepared.Trace.UsedChunkCount == 0
}

// webSourcesToRAGSources转换网络来源为统一来源结构
func webSourcesToRAGSources(sources []websearch.Source) []rag.Source {
	result := make([]rag.Source, 0, len(sources))
	for _, source := range sources {
		result = append(result, rag.Source{
			SourceType:     rag.SourceTypeWeb,
			Title:          source.Title,
			URL:            source.URL,
			ContentPreview: source.Snippet,
		})
	}
	return result
}

func (s *ChatService) resolveRequest(req ChatRequest) (rag.Request, error) {
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		return rag.Request{}, pkgerrors.WithStack(ErrQuestionEmpty)
	}
	topK := s.cfg.RAG.TopK
	if topK <= 0 {
		topK = 5
	}
	if topK > 20 {
		topK = 20
	}

	scoreThreshold := s.cfg.RAG.ScoreThreshold
	if scoreThreshold < 0 {
		scoreThreshold = 0
	}

	maxContextTokens := s.cfg.RAG.MaxContextTokens
	if maxContextTokens <= 0 {
		maxContextTokens = 6000
	}
	if maxContextTokens > 20000 {
		maxContextTokens = 20000
	}

	strictMode := s.cfg.RAG.StrictMode
	queryRewrite := s.cfg.RAG.QueryRewriteEnabled
	queryRewriteMaxQueries := s.cfg.RAG.QueryRewriteMaxQueries
	if queryRewriteMaxQueries <= 0 {
		queryRewriteMaxQueries = 3
	}
	if queryRewriteMaxQueries > 5 {
		queryRewriteMaxQueries = 5
	}
	hybridEnabled := s.cfg.RAG.HybridEnabled
	bm25TopK := s.cfg.RAG.BM25TopK
	if bm25TopK <= 0 {
		bm25TopK = 5
	}
	if bm25TopK > 20 {
		bm25TopK = 20
	}
	rerankEnabled := s.cfg.Rerank.Enabled
	rerankCandidateLimit := s.cfg.Rerank.CandidateLimit
	if rerankCandidateLimit <= 0 {
		rerankCandidateLimit = 20
	}
	if rerankCandidateLimit > 50 {
		rerankCandidateLimit = 50
	}
	rerankTopN := s.cfg.Rerank.TopN
	if rerankTopN <= 0 {
		rerankTopN = 8
	}
	if rerankTopN > rerankCandidateLimit {
		rerankTopN = rerankCandidateLimit
	}

	return rag.Request{
		UserID:                      req.UserID,
		KnowledgeBaseID:             req.KnowledgeBaseID,
		ConversationID:              strings.TrimSpace(req.ConversationID),
		Question:                    req.Question,
		TopK:                        topK,
		ScoreThreshold:              scoreThreshold,
		MaxContextTokens:            maxContextTokens,
		StrictMode:                  strictMode,
		QueryRewrite:                queryRewrite,
		QueryRewriteMaxQueries:      queryRewriteMaxQueries,
		QueryRewriteIncludeOriginal: s.cfg.RAG.QueryRewriteIncludeOriginal,
		HybridEnabled:               hybridEnabled,
		BM25Enabled:                 s.cfg.RAG.BM25Enabled,
		BM25TopK:                    bm25TopK,
		RRFK:                        s.cfg.RAG.RRFK,
		Rerank:                      rerankEnabled,
		RerankCandidateLimit:        rerankCandidateLimit,
		RerankTopN:                  rerankTopN,
		WebSearch:                   req.WebSearch,
	}, nil
}

func applyKnowledgeSearchOptions(req *rag.Request, opts KnowledgeSearchOptions) {
	query := strings.TrimSpace(opts.Query)
	if query != "" {
		req.SearchQuery = query
	}
	if opts.TopK > 0 {
		req.TopK = opts.TopK
	}
	if req.TopK > 20 {
		req.TopK = 20
	}
	if opts.QueryRewrite != nil {
		req.QueryRewrite = *opts.QueryRewrite
	}
	if opts.Hybrid != nil {
		req.HybridEnabled = *opts.Hybrid
	}
	if opts.Rerank != nil {
		req.Rerank = *opts.Rerank
	}
}

func (s *ChatService) saveLog(prepared *rag.PreparedChat, start time.Time) (uint64, error) {
	return s.saveLogWithAgent(prepared, start, nil, "")
}

func (s *ChatService) saveLogWithAgent(prepared *rag.PreparedChat, start time.Time, agentTrace interface{}, answerMode string) (uint64, error) {
	rawSources, _ := json.Marshal(prepared.Sources)
	rawTrace, _ := json.Marshal(prepared.Trace)
	var rawAgentTrace []byte
	if agentTrace != nil {
		rawAgentTrace, _ = json.Marshal(agentTrace)
	}
	log := model.ChatLog{
		KnowledgeBaseID:  prepared.Request.KnowledgeBaseID,
		UserID:           prepared.Request.UserID,
		ConversationID:   prepared.Request.ConversationID,
		Question:         prepared.Request.Question,
		Answer:           prepared.Answer,
		RetrievedChunks:  datatypes.JSON(rawSources),
		RetrievalTrace:   datatypes.JSON(rawTrace),
		AgentTrace:       datatypes.JSON(rawAgentTrace),
		AnswerMode:       answerMode,
		PromptPreview:    prepared.PromptPreview,
		ModelName:        s.chain.ModelName(),
		PromptTokens:     prepared.PromptTokens,
		CompletionTokens: prepared.CompletionTokens,
		ScoreThreshold:   prepared.Request.ScoreThreshold,
		TopK:             prepared.Request.TopK,
		MaxContextTokens: prepared.Request.MaxContextTokens,
		StrictMode:       prepared.Request.StrictMode,
		LatencyMS:        time.Since(start).Milliseconds(),
	}
	if log.PromptTokens == 0 {
		log.PromptTokens = document.EstimateTokens(prepared.Request.Question + prepared.ContextText + prepared.Answer)
	}
	if err := s.logs.Create(&log); err != nil {
		return 0, err
	}
	return log.ID, nil
}
