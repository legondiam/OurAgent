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

	einomodel "github.com/cloudwego/eino/components/model"
	pkgerrors "github.com/pkg/errors"
	"gorm.io/datatypes"
)

type ChatService struct {
	kbs   *repository.KnowledgeBaseRepository
	docs  *repository.DocumentRepository
	logs  *repository.ChatLogRepository
	chain *rag.RAGChain
	cfg   *config.Config
}

type ChatRequest struct {
	UserID           uint64
	KnowledgeBaseID  uint64
	Question         string
	TopK             int
	ScoreThreshold   *float64
	MaxContextTokens int
	StrictMode       *bool
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

func NewChatService(ctx context.Context, kbs *repository.KnowledgeBaseRepository, docs *repository.DocumentRepository, logs *repository.ChatLogRepository, retriever rag.Retriever, chat einomodel.BaseChatModel, cfg *config.Config) (*ChatService, error) {
	ragChain, err := rag.NewRAGChain(ctx, retriever, chat, cfg.LLM.ChatModel)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "初始化 Eino RAG Chain 失败")
	}
	return &ChatService{kbs: kbs, docs: docs, logs: logs, chain: ragChain, cfg: cfg}, nil
}

// Chat 执行知识库检索并生成回答
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
	// 保存问答日志并返回引用来源
	logID, err := s.saveLog(prepared, start)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "保存问答日志失败")
	}
	return &ChatResponse{Answer: prepared.Answer, Sources: prepared.Sources, ChatLogID: logID}, nil
}

// Stream 执行知识库检索并流式生成回答
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
		// 校验阶段已经拒答时直接返回流式事件
		if prepared != nil && prepared.Answer != "" {
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
				out <- StreamEvent{Type: "message", Content: chunk.Content}
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

// ListLogs 查询用户问答日志
func (s *ChatService) ListLogs(userID uint64) ([]model.ChatLog, error) {
	logs, err := s.logs.ListByUserID(userID, 100)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询问答日志失败")
	}
	return logs, nil
}

// SubmitFeedback 保存用户问答反馈
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
	resolved, err := s.resolveRequest(req)
	if err != nil {
		return rag.Request{}, nil, err
	}

	exists, err := s.kbs.ExistsByIDAndUserID(resolved.KnowledgeBaseID, resolved.UserID)
	if err != nil {
		return rag.Request{}, nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	if !exists {
		return rag.Request{}, nil, pkgerrors.WithStack(ErrKnowledgeBaseNotFound)
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

func (s *ChatService) resolveRequest(req ChatRequest) (rag.Request, error) {
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		return rag.Request{}, pkgerrors.WithStack(ErrQuestionEmpty)
	}
	topK := req.TopK
	if topK <= 0 {
		topK = s.cfg.RAG.TopK
	}
	if topK <= 0 {
		topK = 5
	}
	if topK > 20 {
		topK = 20
	}

	scoreThreshold := s.cfg.RAG.ScoreThreshold
	if req.ScoreThreshold != nil {
		scoreThreshold = *req.ScoreThreshold
	}
	if scoreThreshold < 0 {
		scoreThreshold = 0
	}

	maxContextTokens := req.MaxContextTokens
	if maxContextTokens <= 0 {
		maxContextTokens = s.cfg.RAG.MaxContextTokens
	}
	if maxContextTokens <= 0 {
		maxContextTokens = 6000
	}
	if maxContextTokens > 20000 {
		maxContextTokens = 20000
	}

	strictMode := s.cfg.RAG.StrictMode
	if req.StrictMode != nil {
		strictMode = *req.StrictMode
	}

	return rag.Request{
		UserID:           req.UserID,
		KnowledgeBaseID:  req.KnowledgeBaseID,
		Question:         req.Question,
		TopK:             topK,
		ScoreThreshold:   scoreThreshold,
		MaxContextTokens: maxContextTokens,
		StrictMode:       strictMode,
	}, nil
}

func (s *ChatService) saveLog(prepared *rag.PreparedChat, start time.Time) (uint64, error) {
	rawSources, _ := json.Marshal(prepared.Sources)
	rawTrace, _ := json.Marshal(prepared.Trace)
	log := model.ChatLog{
		KnowledgeBaseID:  prepared.Request.KnowledgeBaseID,
		UserID:           prepared.Request.UserID,
		Question:         prepared.Request.Question,
		Answer:           prepared.Answer,
		RetrievedChunks:  datatypes.JSON(rawSources),
		RetrievalTrace:   datatypes.JSON(rawTrace),
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
