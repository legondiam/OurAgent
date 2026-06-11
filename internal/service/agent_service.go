package service

import (
	"context"
	"strings"
	"time"

	"OurAgent/internal/agent"
	"OurAgent/internal/rag"
	"OurAgent/internal/websearch"

	pkgerrors "github.com/pkg/errors"
)

type AgentService struct {
	chat        *ChatService
	planner     agent.Planner
	postPlanner *agent.PostRAGPlanner
}

type AgentChatRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	ConversationID  string
	Question        string
}

type AgentChatResponse struct {
	Answer     string             `json:"answer"`
	Sources    []rag.Source       `json:"sources"`
	Trace      rag.RetrievalTrace `json:"trace"`
	AgentTrace agent.Trace        `json:"agent_trace"`
	ChatLogID  uint64             `json:"chat_log_id"`
}

type ContextLookupResult struct {
	Context       agent.ConversationContext
	SkippedReason string
}

const (
	conversationHistoryLimit       = 5
	conversationAnswerPreviewChars = 300
	conversationMaxHistoryChars    = 3000
)

// NewAgentService创建Agent服务
func NewAgentService(chat *ChatService, planners ...agent.Planner) *AgentService {
	var planner agent.Planner
	if len(planners) > 0 {
		planner = planners[0]
	}
	if planner == nil {
		planner = agent.NewFallbackPlanner()
	}
	return &AgentService{
		chat:        chat,
		planner:     planner,
		postPlanner: agent.NewPostRAGPlanner(),
	}
}

// Chat执行AgentRouter问答流程
func (s *AgentService) Chat(ctx context.Context, req AgentChatRequest) (*AgentChatResponse, error) {
	start := time.Now()
	trace := agent.NewTrace(agent.IntentKnowledgeQA)
	chatReq := ChatRequest{
		UserID:          req.UserID,
		KnowledgeBaseID: req.KnowledgeBaseID,
		ConversationID:  req.ConversationID,
		Question:        req.Question,
	}

	// 先校验知识库归属，避免直接联网路径绕过权限
	resolved, err := s.chat.authorizeRequest(ctx, chatReq)
	if err != nil {
		return nil, err
	}

	// 先由LLMPlanner决定是否检索以及如何检索
	decision := s.plan(ctx, req.Question, req.ConversationID, &trace)
	trace.MarkPlannerDecision(decision)
	if decision.Action == agent.ActionContextLookup {
		decision = s.planWithConversationContext(ctx, req, &trace)
		trace.MarkContextResolvedDecision(decision)
	}

	switch decision.Action {
	case agent.ActionClarify:
		return s.answerClarify(resolved, decision, trace, start)
	case agent.ActionReject:
		return s.answerReject(resolved, decision, trace, start)
	case agent.ActionWebSearch:
		if !s.canUseWebSearch() {
			trace.MarkRejected("联网搜索未启用或未被允许")
			return s.answerReject(resolved, decision, trace, start)
		}
		return s.runWebSearchDirect(ctx, resolved, &trace, start)
	case agent.ActionKnowledgeSearch:
		return s.runKnowledgeSearchWithPlan(ctx, chatReq, decision.SearchPlan, &trace, start)
	case agent.ActionContextLookup:
		fallback := agent.Decision{
			Action:          agent.ActionClarify,
			Reason:          "会话上下文工具未完成最终决策",
			ClarifyQuestion: "请补充你想问的是哪个流程、制度或文档范围。",
		}
		return s.answerClarify(resolved, fallback, trace, start)
	default:
		fallback := agent.DefaultKnowledgeSearchDecision(req.Question, s.defaultSearchPlan(req.Question))
		trace.MarkPlannerDecision(fallback)
		return s.runKnowledgeSearchWithPlan(ctx, chatReq, fallback.SearchPlan, &trace, start)
	}
}

// plan调用Planner并归一化决策
func (s *AgentService) plan(ctx context.Context, question, conversationID string, trace *agent.Trace) agent.Decision {
	defaults := s.defaultSearchPlan(question)
	input := agent.PlannerInput{
		Stage:        agent.PlannerStagePreRAG,
		UserQuestion: question,
		Tools:        s.availableTools(conversationID),
		WebEnabled:   s.canUseWebSearch(),
	}
	decision, err := s.planner.Plan(ctx, input)
	if err != nil {
		trace.MarkPlannerError(err)
		decision = agent.DefaultKnowledgeSearchDecision(question, defaults)
	}
	return agent.NormalizeDecision(decision, input, defaults)
}

// planWithConversationContext读取会话历史并进行二次规划
func (s *AgentService) planWithConversationContext(ctx context.Context, req AgentChatRequest, trace *agent.Trace) agent.Decision {
	contextResult, err := s.lookupConversationContext(ctx, req)
	trace.MarkContextLookup(contextResult.Context.ConversationID, len(contextResult.Context.Messages), err)
	if err != nil || len(contextResult.Context.Messages) == 0 {
		return agent.Decision{
			Action:          agent.ActionClarify,
			Reason:          "缺少可用会话上下文",
			ClarifyQuestion: "请补充你想问的是哪个流程、制度或文档范围。",
		}
	}

	defaults := s.defaultSearchPlan(req.Question)
	input := agent.PlannerInput{
		Stage:        agent.PlannerStageContextResolved,
		UserQuestion: req.Question,
		Tools:        s.availableFinalTools(),
		WebEnabled:   s.canUseWebSearch(),
		Context:      &contextResult.Context,
	}
	decision, err := s.planner.Plan(ctx, input)
	if err != nil {
		trace.MarkPlannerError(err)
		return agent.Decision{
			Action:          agent.ActionClarify,
			Reason:          "会话上下文后二次规划失败",
			ClarifyQuestion: "请补充你想问的是哪个流程、制度或文档范围。",
		}
	}
	return agent.NormalizeDecision(decision, input, defaults)
}

// runKnowledgeSearchWithPlan按Planner检索计划调用知识库RAG
func (s *AgentService) runKnowledgeSearchWithPlan(ctx context.Context, req ChatRequest, plan agent.SearchPlan, trace *agent.Trace, start time.Time) (*AgentChatResponse, error) {
	opts := knowledgeSearchOptionsFromPlan(plan)
	prepared, err := s.runKnowledgeSearch(ctx, req, opts, trace)
	if err != nil {
		return nil, err
	}
	if !agent.IsLowConfidence(prepared) {
		trace.FinalMode = agent.FinalModeKnowledgeBase
		return s.saveAgentResponse(prepared, *trace, start)
	}
	return s.handleLowConfidence(ctx, req, prepared, trace, start)
}

// runKnowledgeSearch调用纯知识库RAG工具
func (s *AgentService) runKnowledgeSearch(ctx context.Context, req ChatRequest, opts KnowledgeSearchOptions, trace *agent.Trace) (*rag.PreparedChat, error) {
	// PrepareKnowledgeAnswerWithOptions不会触发applyWebFallback
	prepared, err := s.chat.PrepareKnowledgeAnswerWithOptions(ctx, req, opts)
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
			"search_query":     prepared.Request.EffectiveSearchQuery(),
		},
	})
	return prepared, nil
}

// handleLowConfidence处理知识库低置信度后的兜底动作
func (s *AgentService) handleLowConfidence(ctx context.Context, req ChatRequest, prepared *rag.PreparedChat, trace *agent.Trace, start time.Time) (*AgentChatResponse, error) {
	decision := s.planAfterRAG(ctx, req.Question, prepared.Trace, trace)
	trace.MarkPostRAGDecision(decision)

	switch decision.Action {
	case agent.ActionClarify:
		trace.MarkClarify(decision.ClarifyQuestion)
		final := clonePrepared(prepared)
		final.Answer = decision.ClarifyQuestion
		final.Sources = []rag.Source{}
		return s.saveAgentResponse(final, *trace, start)
	case agent.ActionWebSearch:
		if !s.canUseWebSearch() {
			trace.AddStep(agent.Step{
				Tool:   agent.ToolWebSearch,
				Action: "invoke",
				Status: agent.StatusSkipped,
				Reason: "联网搜索未启用或未被允许",
			})
			trace.MarkRejected(prepared.Trace.RejectReason)
			return s.saveAgentResponse(prepared, *trace, start)
		}
		webPrepared, err := s.runWebSearch(ctx, prepared, trace)
		if err != nil {
			trace.AddStep(agent.Step{
				Tool:   agent.ToolWebSearch,
				Action: "invoke",
				Status: agent.StatusError,
				Reason: err.Error(),
			})
			trace.MarkRejected(prepared.Trace.RejectReason)
			return s.saveAgentResponse(prepared, *trace, start)
		}
		trace.Intent = agent.IntentWebAugmentedQA
		trace.FinalMode = agent.FinalModeWebFallback
		return s.saveAgentResponse(webPrepared, *trace, start)
	case agent.ActionReject:
		trace.MarkRejected(decision.Reason)
		return s.saveAgentResponse(prepared, *trace, start)
	default:
		trace.AddStep(agent.Step{
			Tool:   agent.ToolWebSearch,
			Action: "invoke",
			Status: agent.StatusSkipped,
			Reason: "Post-RAG Planner返回未知动作",
		})
		trace.MarkRejected(prepared.Trace.RejectReason)
		return s.saveAgentResponse(prepared, *trace, start)
	}
}

// planAfterRAG调用Planner进行低置信度后置决策
func (s *AgentService) planAfterRAG(ctx context.Context, question string, retrievalTrace rag.RetrievalTrace, trace *agent.Trace) agent.Decision {
	input := agent.PlannerInput{
		Stage:        agent.PlannerStagePostRAG,
		UserQuestion: question,
		Tools:        s.availablePostRAGTools(),
		WebEnabled:   s.canUseWebSearch(),
		Observation:  buildRetrievalObservation(retrievalTrace),
	}
	decision, err := s.planner.Plan(ctx, input)
	if err != nil {
		trace.MarkPlannerError(err)
		decision = s.fallbackPostRAGDecision(question, retrievalTrace)
	}
	return agent.NormalizeDecision(decision, input, agent.SearchPlan{})
}

// fallbackPostRAGDecision返回后置Planner失败时的规则兜底
func (s *AgentService) fallbackPostRAGDecision(question string, retrievalTrace rag.RetrievalTrace) agent.Decision {
	clarify := s.postPlanner.ClarifyAfterRAG(question, retrievalTrace)
	if clarify.NeedClarify {
		return agent.Decision{
			Action:          agent.ActionClarify,
			Reason:          clarify.Reason,
			ClarifyQuestion: clarify.Question,
		}
	}
	return agent.DefaultPostRAGDecision(question, s.canUseWebSearch())
}

// runWebSearchDirect直接调用联网搜索工具
func (s *AgentService) runWebSearchDirect(ctx context.Context, resolved rag.Request, trace *agent.Trace, start time.Time) (*AgentChatResponse, error) {
	prepared := &rag.PreparedChat{
		Request: resolved,
		Answer:  rag.FallbackAnswer,
		Sources: []rag.Source{},
		Trace:   rag.NewTrace(resolved, "Agent Router直接选择联网搜索"),
	}
	webPrepared, err := s.runWebSearch(ctx, prepared, trace)
	if err != nil {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolWebSearch,
			Action: "invoke",
			Status: agent.StatusError,
			Reason: err.Error(),
		})
		trace.MarkRejected(err.Error())
		return s.saveAgentResponse(prepared, *trace, start)
	}
	trace.Intent = agent.IntentWebAugmentedQA
	trace.FinalMode = agent.FinalModeWebFallback
	return s.saveAgentResponse(webPrepared, *trace, start)
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

// answerClarify返回Planner澄清问题
func (s *AgentService) answerClarify(resolved rag.Request, decision agent.Decision, trace agent.Trace, start time.Time) (*AgentChatResponse, error) {
	trace.MarkClarify(decision.ClarifyQuestion)
	prepared := &rag.PreparedChat{
		Request: resolved,
		Answer:  decision.ClarifyQuestion,
		Sources: []rag.Source{},
		Trace:   rag.NewTrace(resolved, decision.Reason),
	}
	return s.saveAgentResponse(prepared, trace, start)
}

// answerReject返回Agent拒答
func (s *AgentService) answerReject(resolved rag.Request, decision agent.Decision, trace agent.Trace, start time.Time) (*AgentChatResponse, error) {
	trace.MarkRejected(decision.Reason)
	prepared := &rag.PreparedChat{
		Request: resolved,
		Answer:  rag.FallbackAnswer,
		Sources: []rag.Source{},
		Trace:   rag.NewTrace(resolved, decision.Reason),
	}
	return s.saveAgentResponse(prepared, trace, start)
}

// canUseWebSearch判断Agent是否允许联网降级
func (s *AgentService) canUseWebSearch() bool {
	return s.chat.web != nil && s.chat.cfg.Web.Enabled && s.chat.cfg.Web.FallbackOnly
}

// defaultSearchPlan生成默认检索计划
func (s *AgentService) defaultSearchPlan(question string) agent.SearchPlan {
	topK := s.chat.cfg.RAG.TopK
	if topK <= 0 {
		topK = 5
	}
	if topK > 20 {
		topK = 20
	}
	return agent.SearchPlan{
		Query:               question,
		TopK:                topK,
		QueryRewriteEnabled: s.chat.cfg.RAG.QueryRewriteEnabled,
		HybridEnabled:       s.chat.cfg.RAG.HybridEnabled,
		RerankEnabled:       s.chat.cfg.Rerank.Enabled,
		Reason:              "使用服务端默认检索配置",
	}
}

// availableTools返回Planner可选择的工具
func (s *AgentService) availableTools(conversationID string) []agent.ToolSpec {
	tools := []agent.ToolSpec{
		{Name: string(agent.ActionClarify), Description: "问题缺少关键业务对象时先追问用户"},
		{Name: string(agent.ActionKnowledgeSearch), Description: "查询企业知识库并基于来源回答"},
		{Name: string(agent.ActionReject), Description: "问题不适合当前系统处理时拒答"},
	}
	if strings.TrimSpace(conversationID) != "" {
		tools = append([]agent.ToolSpec{
			{Name: string(agent.ActionContextLookup), Description: "当前问题依赖上一轮语义时读取会话历史"},
		}, tools...)
	}
	if s.canUseWebSearch() {
		tools = append(tools, agent.ToolSpec{Name: string(agent.ActionWebSearch), Description: "查询实时或公开网络信息"})
	}
	return tools
}

// availableFinalTools返回不含上下文工具的最终动作列表
func (s *AgentService) availableFinalTools() []agent.ToolSpec {
	tools := []agent.ToolSpec{
		{Name: string(agent.ActionClarify), Description: "问题缺少关键业务对象时先追问用户"},
		{Name: string(agent.ActionKnowledgeSearch), Description: "查询企业知识库并基于来源回答"},
		{Name: string(agent.ActionReject), Description: "问题不适合当前系统处理时拒答"},
	}
	if s.canUseWebSearch() {
		tools = append(tools, agent.ToolSpec{Name: string(agent.ActionWebSearch), Description: "查询实时或公开网络信息"})
	}
	return tools
}

// availablePostRAGTools返回低置信度后可选择的工具
func (s *AgentService) availablePostRAGTools() []agent.ToolSpec {
	tools := []agent.ToolSpec{
		{Name: string(agent.ActionClarify), Description: "检索结果不足且问题缺少关键对象时追问用户"},
		{Name: string(agent.ActionReject), Description: "知识库没有可靠依据且不能继续补充时拒答"},
	}
	if s.canUseWebSearch() {
		tools = append(tools, agent.ToolSpec{Name: string(agent.ActionWebSearch), Description: "知识库无可靠依据时查询实时或公开网络信息"})
	}
	return tools
}

// lookupConversationContext读取并裁剪会话历史
func (s *AgentService) lookupConversationContext(ctx context.Context, req AgentChatRequest) (ContextLookupResult, error) {
	conversationID := strings.TrimSpace(req.ConversationID)
	result := ContextLookupResult{
		Context: agent.ConversationContext{
			ConversationID: conversationID,
			Messages:       []agent.HistoryMessage{},
		},
	}
	if conversationID == "" {
		result.SkippedReason = "conversation_id为空"
		return result, nil
	}
	logs, err := s.chat.logs.ListRecentByConversation(req.UserID, req.KnowledgeBaseID, conversationID, conversationHistoryLimit)
	if err != nil {
		return result, err
	}
	if len(logs) == 0 {
		result.SkippedReason = "没有可用会话历史"
		return result, nil
	}
	totalChars := 0
	for i := len(logs) - 1; i >= 0; i-- {
		question := strings.TrimSpace(logs[i].Question)
		answer := truncateRunes(strings.TrimSpace(logs[i].Answer), conversationAnswerPreviewChars)
		itemChars := runeCount(question) + runeCount(answer)
		if totalChars > 0 && totalChars+itemChars > conversationMaxHistoryChars {
			continue
		}
		totalChars += itemChars
		result.Context.Messages = append(result.Context.Messages, agent.HistoryMessage{
			Question: question,
			Answer:   answer,
		})
	}
	return result, nil
}

func buildRetrievalObservation(trace rag.RetrievalTrace) *agent.RetrievalObservation {
	queries := make([]string, 0, len(trace.RewrittenQueries))
	for _, query := range trace.RewrittenQueries {
		queries = append(queries, query.Query)
	}
	hits := make([]agent.RetrievalHitSummary, 0, minInt(len(trace.Hits), 5))
	for i, hit := range trace.Hits {
		if i >= 5 {
			break
		}
		hits = append(hits, agent.RetrievalHitSummary{
			DocumentName: hit.DocumentName,
			SectionPath:  hit.SectionPath,
			Score:        hit.Score,
			Used:         hit.Used,
			Reason:       hit.Reason,
		})
	}
	return &agent.RetrievalObservation{
		SearchQuery:      trace.Query,
		RewrittenQueries: queries,
		UsedChunkCount:   trace.UsedChunkCount,
		RejectReason:     trace.RejectReason,
		TopHits:          hits,
	}
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func runeCount(text string) int {
	return len([]rune(text))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func knowledgeSearchOptionsFromPlan(plan agent.SearchPlan) KnowledgeSearchOptions {
	return KnowledgeSearchOptions{
		QueryRewrite: boolPtr(plan.QueryRewriteEnabled),
		Hybrid:       boolPtr(plan.HybridEnabled),
		Rerank:       boolPtr(plan.RerankEnabled),
		TopK:         plan.TopK,
		Query:        plan.Query,
	}
}

func boolPtr(v bool) *bool {
	return &v
}

// saveAgentResponse保存Agent回答并组装响应
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

// clonePrepared复制RAG结果避免修改原对象
func clonePrepared(prepared *rag.PreparedChat) *rag.PreparedChat {
	if prepared == nil {
		return &rag.PreparedChat{}
	}
	cloned := *prepared
	return &cloned
}
