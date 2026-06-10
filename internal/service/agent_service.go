package service

import (
	"context"
	"time"

	"OurAgent/internal/agent"
	"OurAgent/internal/rag"
	"OurAgent/internal/websearch"

	pkgerrors "github.com/pkg/errors"
)

type AgentService struct {
	chat    *ChatService
	planner *agent.Planner
}

type AgentChatRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
}

type AgentChatResponse struct {
	Answer     string             `json:"answer"`
	Sources    []rag.Source       `json:"sources"`
	Trace      rag.RetrievalTrace `json:"trace"`
	AgentTrace agent.Trace        `json:"agent_trace"`
	ChatLogID  uint64             `json:"chat_log_id"`
}

// NewAgentService创建Agent服务
func NewAgentService(chat *ChatService) *AgentService {
	return &AgentService{
		chat:    chat,
		planner: agent.NewPlanner(),
	}
}

// Chat执行Agent问答流程
func (s *AgentService) Chat(ctx context.Context, req AgentChatRequest) (*AgentChatResponse, error) {
	start := time.Now()
	trace := agent.NewTrace(agent.IntentKnowledgeQA)
	chatReq := ChatRequest{
		UserID:          req.UserID,
		KnowledgeBaseID: req.KnowledgeBaseID,
		Question:        req.Question,
	}

	// 先走纯知识库RAG，让query rewrite和检索链路完整执行
	prepared, err := s.runKnowledgeSearch(ctx, chatReq, &trace)
	if err != nil {
		return nil, err
	}

	// 知识库证据充足时直接返回，不触发后续工具
	if !agent.IsLowConfidence(prepared) {
		trace.FinalMode = agent.FinalModeKnowledgeBase
		return s.saveAgentResponse(prepared, trace, start)
	}

	// 低置信度后再判断是否需要澄清，避免提前绕过query rewrite
	clarify := s.planner.ClarifyAfterRAG(req.Question, prepared.Trace)
	if clarify.NeedClarify {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolKnowledgeSearch,
			Action: "clarify_after_rag",
			Status: agent.StatusSkipped,
			Reason: clarify.Reason,
		})
		trace.MarkClarify(clarify.Question)
		final := clonePrepared(prepared)
		final.Answer = clarify.Question
		final.Sources = []rag.Source{}
		return s.saveAgentResponse(final, trace, start)
	}

	// 服务端未允许联网时保持知识库拒答结果
	if !s.canUseWebSearch() {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolWebSearch,
			Action: "invoke",
			Status: agent.StatusSkipped,
			Reason: "联网搜索未启用或未被允许",
		})
		trace.MarkRejected(prepared.Trace.RejectReason)
		return s.saveAgentResponse(prepared, trace, start)
	}

	// 低置信度且允许联网时调用WebSearchTool补充答案
	webPrepared, err := s.runWebSearch(ctx, prepared, &trace)
	if err != nil {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolWebSearch,
			Action: "invoke",
			Status: agent.StatusError,
			Reason: err.Error(),
		})
		trace.MarkRejected(prepared.Trace.RejectReason)
		return s.saveAgentResponse(prepared, trace, start)
	}

	trace.Intent = agent.IntentWebAugmentedQA
	trace.FinalMode = agent.FinalModeWebFallback
	return s.saveAgentResponse(webPrepared, trace, start)
}

// runKnowledgeSearch调用纯知识库RAG工具
func (s *AgentService) runKnowledgeSearch(ctx context.Context, req ChatRequest, trace *agent.Trace) (*rag.PreparedChat, error) {
	// PrepareKnowledgeAnswer不会触发applyWebFallback
	prepared, err := s.chat.PrepareKnowledgeAnswer(ctx, req)
	if err != nil {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolKnowledgeSearch,
			Action: "invoke",
			Status: agent.StatusError,
			Reason: err.Error(),
		})
		return nil, err
	}
	status := agent.StatusSuccess
	if agent.IsLowConfidence(prepared) {
		status = agent.StatusLowConfidence
	}
	// 记录检索结果摘要，便于排查Agent决策来源
	trace.AddStep(agent.Step{
		Tool:   agent.ToolKnowledgeSearch,
		Action: "invoke",
		Status: status,
		Reason: prepared.Trace.RejectReason,
		Metadata: map[string]any{
			"used_chunk_count": prepared.Trace.UsedChunkCount,
			"rewrite_enabled":  prepared.Trace.RewriteEnabled,
			"query_count":      len(prepared.Trace.RewrittenQueries),
		},
	})
	return prepared, nil
}

// runWebSearch调用联网搜索工具
func (s *AgentService) runWebSearch(ctx context.Context, prepared *rag.PreparedChat, trace *agent.Trace) (*rag.PreparedChat, error) {
	// WebSearchTool直接调用联网Answerer，不复用ChatService的fallback包装
	result, err := s.chat.web.Answer(ctx, websearch.Request{
		UserID:          prepared.Request.UserID,
		KnowledgeBaseID: prepared.Request.KnowledgeBaseID,
		Question:        prepared.Request.Question,
	})
	if err != nil {
		return nil, err
	}

	// 基于原RAG结果复制一份，保留原始trace并补充联网字段
	final := clonePrepared(prepared)
	final.Answer = result.Answer
	final.Sources = webSourcesToRAGSources(result.Sources)
	final.Trace.WebFallbackEnabled = true
	final.Trace.WebFallbackUsed = true
	final.Trace.WebFallbackReason = prepared.Trace.RejectReason
	final.Trace.WebSearchProvider = s.chat.cfg.Web.Provider
	final.Trace.WebSearchModel = s.chat.cfg.Web.Model
	final.Trace.WebSearchResultCount = len(result.Sources)

	trace.AddStep(agent.Step{
		Tool:   agent.ToolWebSearch,
		Action: "invoke",
		Status: agent.StatusSuccess,
		Metadata: map[string]any{
			"source_count": len(result.Sources),
			"provider":     s.chat.cfg.Web.Provider,
		},
	})
	return final, nil
}

// canUseWebSearch 判断Agent是否允许联网降级
func (s *AgentService) canUseWebSearch() bool {
	return s.chat.web != nil && s.chat.cfg.Web.Enabled && s.chat.cfg.Web.FallbackOnly
}

// saveAgentResponse 保存Agent回答并组装响应
func (s *AgentService) saveAgentResponse(prepared *rag.PreparedChat, trace agent.Trace, start time.Time) (*AgentChatResponse, error) {
	logID, err := s.chat.saveLogWithAgent(prepared, start, trace, trace.FinalMode)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "保存Agent问答日志失败")
	}
	return &AgentChatResponse{
		Answer:     prepared.Answer,
		Sources:    prepared.Sources,
		Trace:      prepared.Trace,
		AgentTrace: trace,
		ChatLogID:  logID,
	}, nil
}

// clonePrepared 复制RAG结果避免修改原对象
func clonePrepared(prepared *rag.PreparedChat) *rag.PreparedChat {
	if prepared == nil {
		return &rag.PreparedChat{}
	}
	cloned := *prepared
	return &cloned
}
