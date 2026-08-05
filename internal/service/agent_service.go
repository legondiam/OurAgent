package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"OurAgent/internal/agent"
	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/rag"
	"OurAgent/internal/repository"
	"OurAgent/internal/websearch"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
)

type AgentService struct {
	chat               *ChatService
	planner            agent.Planner
	directChat         einomodel.BaseChatModel
	postPlanner        *agent.PostRAGPlanner
	conversations      *repository.ConversationRepository
	contextAssembler   *ConversationContextAssembler
	memoryCfg          config.AgentMemoryConfig
	longTermCfg        config.LongTermMemoryConfig
	longTermRetriever  *LongTermMemoryRetriever
	directiveMatcher   MemoryDirectiveMatcher
	directiveProcessor *MemoryDirectiveProcessor
}

// ConfigureShortTermMemory启用新的短期记忆链路
func (s *AgentService) ConfigureShortTermMemory(conversations *repository.ConversationRepository, assembler *ConversationContextAssembler, cfg config.AgentMemoryConfig) {
	s.conversations = conversations
	s.contextAssembler = assembler
	s.memoryCfg = cfg
}

// ConfigureLongTermMemory 配置Agent长期记忆内部组件
func (s *AgentService) ConfigureLongTermMemory(retriever *LongTermMemoryRetriever, processor *MemoryDirectiveProcessor, cfg config.LongTermMemoryConfig) {
	s.longTermRetriever = retriever
	s.directiveProcessor = processor
	s.longTermCfg = cfg
}

type AgentChatRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	ConversationID  string
	Question        string
}

type AgentChatResponse struct {
	ConversationID string             `json:"conversation_id"`
	Answer         string             `json:"answer"`
	Sources        []rag.Source       `json:"sources"`
	Trace          rag.RetrievalTrace `json:"trace"`
	AgentTrace     agent.Trace        `json:"agent_trace"`
	ChatLogID      uint64             `json:"chat_log_id"`
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

const directAnswerSystemPrompt = `你是企业知识库助手。
当前问题已被Agent判断为不需要查询企业知识库，也不需要联网。
请直接回答用户问题。

要求：
1. 不要编造企业内部制度、流程、文档或数据
2. 如果问题实际涉及企业内部事实，请说明需要查询知识库
3. 回答简洁清晰`

const conversationAnswerSystemPrompt = `你是企业知识库助手。
你的任务是严格按照用户当前要求，对提供的既有会话回答进行复述、缩写、翻译、整理或比较。

要求：
1. 只能使用提供的既有回答，不要补充新的企业事实
2. 保留原回答中的条件、限制和不确定性
3. 不要声称重新查询了知识库
4. 直接输出处理后的结果`

// NewAgentService创建Agent服务
func NewAgentService(chat *ChatService, planner agent.Planner, directChats ...einomodel.BaseChatModel) *AgentService {
	var directChat einomodel.BaseChatModel
	if len(directChats) > 0 {
		directChat = directChats[0]
	}
	if planner == nil {
		planner = agent.NewFallbackPlanner()
	}
	return &AgentService{
		chat:        chat,
		planner:     planner,
		directChat:  directChat,
		postPlanner: agent.NewPostRAGPlanner(),
	}
}

// Chat执行AgentRouter问答流程
func (s *AgentService) Chat(ctx context.Context, req AgentChatRequest) (*AgentChatResponse, error) {
	start := time.Now()
	trace := agent.NewTrace(agent.IntentKnowledgeQA)
	preflight, err := s.chat.authorizeRequest(ctx, ChatRequest{UserID: req.UserID, KnowledgeBaseID: req.KnowledgeBaseID, Question: req.Question, OriginalQuestion: req.Question, AgentTurn: true})
	if err != nil {
		return nil, err
	}
	directivePossible := s.longTermCfg.Enabled && s.longTermCfg.DirectiveEnabled && s.directiveProcessor != nil && s.directiveMatcher.MayContainDirective(req.Question)
	type memoryRetrieveResult struct {
		context agent.LongTermMemoryContext
		err     error
	}
	var memoryResult <-chan memoryRetrieveResult
	if s.longTermCfg.Enabled && s.longTermRetriever != nil && !directivePossible {
		resultChannel := make(chan memoryRetrieveResult, 1)
		memoryResult = resultChannel
		go func(question string) {
			memoryContext, retrieveErr := s.longTermRetriever.Retrieve(ctx, LongTermMemoryRetrieveRequest{UserID: req.UserID, KnowledgeBaseID: req.KnowledgeBaseID, Question: question})
			resultChannel <- memoryRetrieveResult{context: memoryContext, err: retrieveErr}
		}(req.Question)
	}

	conversationID := ""
	var latest *model.ChatLog
	var conversationContext *agent.ConversationContext
	processingToken := ""
	isNewConversation := false
	err = nil
	if s.memoryCfg.ShortTermEnabled && s.conversations != nil && s.contextAssembler != nil {
		conversationID, isNewConversation, processingToken, conversationContext, err = s.prepareShortTermConversation(req)
	} else {
		conversationID, latest, err = s.resolveConversation(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	if processingToken != "" {
		defer s.conversations.ReleaseProcessingLease(conversationID, processingToken)
	}
	req.ConversationID = conversationID
	trace.ConversationID = conversationID
	if conversationContext != nil {
		trace.MarkContextAssembled(*conversationContext)
	}

	chatReq := ChatRequest{
		UserID:                      req.UserID,
		KnowledgeBaseID:             req.KnowledgeBaseID,
		ConversationID:              conversationID,
		Question:                    req.Question,
		OriginalQuestion:            req.Question,
		ConversationProcessingToken: processingToken,
		NewConversation:             isNewConversation,
		AgentTurn:                   true,
	}

	resolved := preflight
	resolved.ConversationID = conversationID
	resolved.ConversationProcessingToken = processingToken
	resolved.NewConversation = isNewConversation
	if directivePossible {
		if trace.Memory == nil {
			trace.Memory = &agent.MemoryTrace{Enabled: true}
		}
		trace.Memory.DirectiveDetected = true
		directive, directiveErr := s.directiveProcessor.Process(ctx, req.Question)
		if directiveErr != nil {
			return nil, directiveErr
		}
		if directive.Operation != nil {
			resolved.PendingMemoryOperation = directive.Operation
			chatReq.PendingMemoryOperation = directive.Operation
			if directive.ResidualQuestion == "" {
				trace.FinalMode = agent.FinalModeDirectAnswer
				prepared := &rag.PreparedChat{Request: resolved, Answer: directive.Confirmation, Sources: []rag.Source{}, Trace: rag.NewTrace(resolved, "显式长期记忆指令")}
				return s.saveAgentResponse(prepared, trace, start)
			}
			req.Question = directive.ResidualQuestion
			chatReq.Question = directive.ResidualQuestion
			resolved.Question = directive.ResidualQuestion
		} else if directive.Handled && directive.ResidualQuestion == "" {
			trace.FinalMode = agent.FinalModeDirectAnswer
			prepared := &rag.PreparedChat{Request: resolved, Answer: directive.Confirmation, Sources: []rag.Source{}, Trace: rag.NewTrace(resolved, "长期记忆指令未执行")}
			return s.saveAgentResponse(prepared, trace, start)
		}
	}
	var longTermContext *agent.LongTermMemoryContext
	if s.longTermCfg.Enabled && s.longTermRetriever != nil {
		var memoryContext agent.LongTermMemoryContext
		var retrieveErr error
		if memoryResult != nil {
			result := <-memoryResult
			memoryContext, retrieveErr = result.context, result.err
		} else {
			memoryContext, retrieveErr = s.longTermRetriever.Retrieve(ctx, LongTermMemoryRetrieveRequest{UserID: req.UserID, KnowledgeBaseID: req.KnowledgeBaseID, Question: req.Question})
		}
		if retrieveErr == nil {
			longTermContext = &memoryContext
			trace.MarkLongTermMemory(memoryContext)
		} else {
			degraded := agent.LongTermMemoryContext{SemanticRetrievalDegraded: true}
			longTermContext = &degraded
			trace.MarkLongTermMemory(degraded)
		}
	}
	if s.longTermCfg.Enabled {
		if trace.Memory == nil {
			trace.Memory = &agent.MemoryTrace{Enabled: true}
		}
		trace.Memory.WorthinessSignal = string((MemoryWorthinessGate{}).Evaluate(persistedAgentQuestion(resolved)))
	}

	var decision agent.Decision
	if conversationContext != nil {
		decision = s.planWithAssembledContext(ctx, req.Question, *conversationContext, longTermContext, &trace)
		trace.MarkContextResolvedDecision(decision)
	} else if latest != nil && latest.AnswerMode == agent.FinalModeClarify {
		decision = s.planWithConversationContext(ctx, req, longTermContext, &trace)
		trace.MarkContextResolvedDecision(decision)
	} else {
		plannerConversationID := ""
		if latest != nil {
			plannerConversationID = conversationID
		}
		// 先由LLMPlanner决定是否检索以及如何检索
		decision = s.plan(ctx, req.Question, plannerConversationID, longTermContext, &trace)
		trace.MarkPlannerDecision(decision)
		if decision.Action == agent.ActionContextLookup {
			decision = s.planWithConversationContext(ctx, req, longTermContext, &trace)
			trace.MarkContextResolvedDecision(decision)
		}
	}
	if decision.Action == agent.ActionKnowledgeProbe {
		decision = s.planWithKnowledgeProbe(ctx, chatReq, decision, longTermContext, &trace)
		trace.MarkProbeResolvedDecision(decision)
	}

	switch decision.Action {
	case agent.ActionClarify:
		return s.answerClarify(resolved, decision, trace, start)
	case agent.ActionReject:
		return s.answerReject(resolved, decision, trace, start)
	case agent.ActionDirectAnswer:
		return s.runDirectAnswer(ctx, resolved, decision, &trace, start)
	case agent.ActionConversationAnswer:
		return s.runConversationAnswer(ctx, resolved, decision, &trace, start)
	case agent.ActionWebSearch:
		if !s.canUseWebSearch() {
			trace.MarkRejected("联网搜索未启用或未被允许")
			return s.answerReject(resolved, decision, trace, start)
		}
		return s.runWebSearchDirect(ctx, resolved, &trace, start)
	case agent.ActionKnowledgeSearch:
		return s.runKnowledgeSearchWithPlan(ctx, chatReq, decision.SearchPlan, &trace, start)
	case agent.ActionKnowledgeProbe:
		fallback := agent.Decision{
			Action:          agent.ActionClarify,
			Reason:          "知识库探测工具未完成最终决策",
			ClarifyQuestion: "请补充你想查询的具体业务对象或文档范围。",
		}
		return s.answerClarify(resolved, fallback, trace, start)
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

func (s *AgentService) prepareShortTermConversation(req AgentChatRequest) (string, bool, string, *agent.ConversationContext, error) {
	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		return newConversationID(), true, "", nil, nil
	}
	if len(conversationID) > 64 {
		return "", false, "", nil, ErrConversationNotFound
	}
	conversation, err := s.conversations.FindOwned(req.UserID, req.KnowledgeBaseID, conversationID)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, "", nil, ErrConversationNotFound
		}
		return "", false, "", nil, err
	}
	now := time.Now()
	if conversation.Status != model.ConversationStatusActive || !conversation.ExpiresAt.After(now) {
		return "", false, "", nil, ErrConversationExpired
	}
	token := uuid.NewString()
	leaseSeconds := s.memoryCfg.ConversationProcessingLeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = 180
	}
	claimed, err := s.conversations.TryAcquireProcessingLease(req.UserID, req.KnowledgeBaseID, conversationID, token, now, now.Add(time.Duration(leaseSeconds)*time.Second))
	if err != nil {
		return "", false, "", nil, err
	}
	if !claimed {
		return "", false, "", nil, ErrConversationBusy
	}
	context, err := s.contextAssembler.Build(ContextAssembleRequest{
		UserID: req.UserID, KnowledgeBaseID: req.KnowledgeBaseID, ConversationID: conversationID,
	})
	if err != nil {
		_ = s.conversations.ReleaseProcessingLease(conversationID, token)
		return "", false, "", nil, err
	}
	return conversationID, false, token, &context, nil
}

func (s *AgentService) planWithAssembledContext(ctx context.Context, question string, conversationContext agent.ConversationContext, longTermContext *agent.LongTermMemoryContext, trace *agent.Trace) agent.Decision {
	defaults := s.defaultSearchPlan(question)
	input := agent.PlannerInput{
		Stage:          agent.PlannerStageContextResolved,
		UserQuestion:   question,
		Tools:          s.availableContextResolvedTools(),
		WebEnabled:     s.canUseWebSearch(),
		Context:        &conversationContext,
		LongTermMemory: longTermContext,
	}
	decision, err := s.planner.Plan(ctx, input)
	if err != nil {
		trace.MarkPlannerError(err)
		return agent.DefaultKnowledgeSearchDecision(question, defaults)
	}
	return agent.NormalizeDecision(decision, input, defaults)
}

// resolveConversation 托管Agent会话ID生命周期
func (s *AgentService) resolveConversation(ctx context.Context, req AgentChatRequest) (string, *model.ChatLog, error) {
	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		return newConversationID(), nil, nil
	}
	if len(conversationID) > 64 {
		return "", nil, ErrConversationNotFound
	}
	latest, err := s.chat.logs.FindLatestByConversation(req.UserID, req.KnowledgeBaseID, conversationID)
	if err != nil {
		return "", nil, err
	}
	if latest == nil {
		return "", nil, ErrConversationNotFound
	}
	return conversationID, latest, nil
}

func newConversationID() string {
	return "conv_" + uuid.NewString()
}

// persistedAgentQuestion 返回需要持久化的原始用户问题
func persistedAgentQuestion(req rag.Request) string {
	if strings.TrimSpace(req.OriginalQuestion) != "" {
		return req.OriginalQuestion
	}
	return req.Question
}

// plan调用Planner并归一化决策
func (s *AgentService) plan(ctx context.Context, question, conversationID string, longTermContext *agent.LongTermMemoryContext, trace *agent.Trace) agent.Decision {
	defaults := s.defaultSearchPlan(question)
	input := agent.PlannerInput{
		Stage:          agent.PlannerStagePreRAG,
		UserQuestion:   question,
		Tools:          s.availableTools(conversationID),
		WebEnabled:     s.canUseWebSearch(),
		LongTermMemory: longTermContext,
	}
	decision, err := s.planner.Plan(ctx, input)
	if err != nil {
		trace.MarkPlannerError(err)
		decision = agent.DefaultKnowledgeSearchDecision(question, defaults)
	}
	return agent.NormalizeDecision(decision, input, defaults)
}

// planWithConversationContext读取会话历史并进行二次规划
func (s *AgentService) planWithConversationContext(ctx context.Context, req AgentChatRequest, longTermContext *agent.LongTermMemoryContext, trace *agent.Trace) agent.Decision {
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
		Stage:          agent.PlannerStageContextResolved,
		UserQuestion:   req.Question,
		Tools:          s.availableFinalTools(),
		WebEnabled:     s.canUseWebSearch(),
		Context:        &contextResult.Context,
		LongTermMemory: longTermContext,
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

// planWithKnowledgeProbe 执行轻量探测并进行二次规划
func (s *AgentService) planWithKnowledgeProbe(ctx context.Context, req ChatRequest, probeDecision agent.Decision, longTermContext *agent.LongTermMemoryContext, trace *agent.Trace) agent.Decision {
	query := strings.TrimSpace(probeDecision.SearchPlan.Query)
	if query == "" {
		query = req.Question
	}
	result, err := s.runKnowledgeProbe(ctx, req, query, trace)
	if err != nil {
		trace.MarkPlannerError(err)
		return agent.Decision{
			Action:          agent.ActionClarify,
			Reason:          "知识库轻量探测失败",
			ClarifyQuestion: "请补充你想查询的具体业务对象或文档范围。",
		}
	}

	evidence := agent.EvaluateProbeEvidence(req.Question, result)
	trace.MarkProbeEvidence(evidence)
	defaults := s.defaultSearchPlan(result.Query)
	input := agent.PlannerInput{
		Stage:          agent.PlannerStageProbeResolved,
		UserQuestion:   req.Question,
		Tools:          s.availableProbeResolvedTools(),
		WebEnabled:     s.canUseWebSearch(),
		ProbeResult:    &result,
		ProbeEvidence:  &evidence,
		LongTermMemory: longTermContext,
	}
	decision, err := s.planner.Plan(ctx, input)
	if err != nil {
		trace.MarkPlannerError(err)
		return agent.Decision{
			Action:          agent.ActionClarify,
			Reason:          "知识库探测后二次规划失败",
			ClarifyQuestion: "请补充你想查询的具体业务对象或文档范围。",
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

// runKnowledgeProbe 调用轻量知识库探测工具
func (s *AgentService) runKnowledgeProbe(ctx context.Context, req ChatRequest, query string, trace *agent.Trace) (agent.KnowledgeProbeResult, error) {
	result, err := s.chat.ProbeKnowledge(ctx, req, query, 3)
	if err != nil {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolKnowledgeProbe,
			Action: "probe",
			Status: agent.StatusError,
			Reason: err.Error(),
			Metadata: map[string]any{
				"query": query,
			},
		})
		return result, err
	}
	trace.AddStep(agent.Step{
		Tool:   agent.ToolKnowledgeProbe,
		Action: "probe",
		Status: agent.StatusSuccess,
		Metadata: map[string]any{
			"query":     result.Query,
			"hit_count": len(result.Hits),
			"max_score": result.MaxScore,
		},
	})
	return result, nil
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

// runDirectAnswer调用模型直接回答通用问题
func (s *AgentService) runDirectAnswer(ctx context.Context, resolved rag.Request, decision agent.Decision, trace *agent.Trace, start time.Time) (*AgentChatResponse, error) {
	if s.directChat == nil {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolDirectAnswer,
			Action: "invoke",
			Status: agent.StatusError,
			Reason: "直接回答模型未配置",
		})
		reject := agent.Decision{Action: agent.ActionReject, Reason: "直接回答模型未配置"}
		return s.answerReject(resolved, reject, *trace, start)
	}
	resp, err := s.directChat.Generate(ctx, []*schema.Message{
		schema.SystemMessage(directAnswerSystemPrompt),
		schema.UserMessage(buildDirectAnswerPrompt(resolved.Question)),
	})
	if err != nil {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolDirectAnswer,
			Action: "invoke",
			Status: agent.StatusError,
			Reason: err.Error(),
		})
		reject := agent.Decision{Action: agent.ActionReject, Reason: "直接回答失败"}
		return s.answerReject(resolved, reject, *trace, start)
	}
	answer := strings.TrimSpace(resp.Content)
	if answer == "" {
		trace.AddStep(agent.Step{
			Tool:   agent.ToolDirectAnswer,
			Action: "invoke",
			Status: agent.StatusError,
			Reason: "模型返回空回答",
		})
		reject := agent.Decision{Action: agent.ActionReject, Reason: "直接回答为空"}
		return s.answerReject(resolved, reject, *trace, start)
	}
	trace.AddStep(agent.Step{
		Tool:   agent.ToolDirectAnswer,
		Action: "invoke",
		Status: agent.StatusSuccess,
		Reason: decision.Reason,
	})
	trace.FinalMode = agent.FinalModeDirectAnswer
	prepared := &rag.PreparedChat{
		Request: resolved,
		Answer:  answer,
		Sources: []rag.Source{},
		Trace:   rag.NewTrace(resolved, decision.Reason),
	}
	return s.saveAgentResponse(prepared, *trace, start)
}

// runConversationAnswer基于既有会话回答执行内容转换
func (s *AgentService) runConversationAnswer(ctx context.Context, resolved rag.Request, decision agent.Decision, trace *agent.Trace, start time.Time) (*AgentChatResponse, error) {
	if s.directChat == nil {
		return s.answerClarify(resolved, agent.Decision{Action: agent.ActionClarify, Reason: "会话回答模型未配置", ClarifyQuestion: "请稍后重试。"}, *trace, start)
	}
	ids := uniqueUint64(decision.SourceChatLogIDs, 5)
	logs, err := s.chat.logs.FindManyOwnedByConversation(resolved.UserID, resolved.KnowledgeBaseID, resolved.ConversationID, ids)
	if err != nil || len(logs) != len(ids) {
		return s.answerClarify(resolved, agent.Decision{Action: agent.ActionClarify, Reason: "会话回答来源不可用", ClarifyQuestion: "请说明你想复述、转换或比较哪一条回答。"}, *trace, start)
	}
	var prompt strings.Builder
	prompt.WriteString("用户当前要求：\n")
	prompt.WriteString(resolved.Question)
	prompt.WriteString("\n\n既有会话回答：\n")
	sources := make([]rag.Source, 0)
	for _, log := range logs {
		prompt.WriteString("\n[chat_log_id=")
		prompt.WriteString(uintString(log.ID))
		prompt.WriteString("]\n用户问题：")
		prompt.WriteString(log.Question)
		prompt.WriteString("\n助手回答：")
		prompt.WriteString(log.Answer)
		var logSources []rag.Source
		if len(log.RetrievedChunks) > 0 && json.Unmarshal(log.RetrievedChunks, &logSources) == nil {
			sources = append(sources, logSources...)
		}
	}
	resp, err := s.directChat.Generate(ctx, []*schema.Message{
		schema.SystemMessage(conversationAnswerSystemPrompt),
		schema.UserMessage(prompt.String()),
	})
	if err != nil || strings.TrimSpace(resp.Content) == "" {
		if err != nil {
			trace.MarkPlannerError(err)
		}
		return s.answerClarify(resolved, agent.Decision{Action: agent.ActionClarify, Reason: "会话回答转换失败", ClarifyQuestion: "请稍后重试，或重新描述希望如何处理上一条回答。"}, *trace, start)
	}
	trace.AddStep(agent.Step{
		Tool:   agent.ToolConversationAnswer,
		Action: "transform",
		Status: agent.StatusSuccess,
		Reason: decision.Reason,
		Metadata: map[string]any{
			"source_chat_log_ids": ids,
			"reused_sources":      len(sources) > 0,
		},
	})
	trace.FinalMode = agent.FinalModeConversationAnswer
	prepared := &rag.PreparedChat{
		Request: resolved,
		Answer:  strings.TrimSpace(resp.Content),
		Sources: dedupeRAGSources(sources),
		Trace:   rag.NewTrace(resolved, decision.Reason),
	}
	return s.saveAgentResponse(prepared, *trace, start)
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
		{Name: string(agent.ActionKnowledgeProbe), Description: "问题可能涉及企业制度、流程、产品或业务对象但不确定知识库是否有资料时轻量探测知识库"},
		{Name: string(agent.ActionDirectAnswer), Description: "回答寒暄、通用知识解释、写作辅助或格式转换等不依赖知识库和联网的问题"},
		{Name: string(agent.ActionClarify), Description: "问题缺少可检索对象或必须补充范围时先追问用户"},
		{Name: string(agent.ActionKnowledgeSearch), Description: "查询企业知识库并基于来源回答"},
		{Name: string(agent.ActionReject), Description: "请求高风险操作、敏感数据外发、绕过权限或不适合当前系统处理时拒答"},
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
		{Name: string(agent.ActionKnowledgeProbe), Description: "问题可能涉及企业制度、流程、产品或业务对象但不确定知识库是否有资料时轻量探测知识库"},
		{Name: string(agent.ActionDirectAnswer), Description: "回答寒暄、通用知识解释、写作辅助或格式转换等不依赖知识库和联网的问题"},
		{Name: string(agent.ActionClarify), Description: "问题缺少可检索对象或必须补充范围时先追问用户"},
		{Name: string(agent.ActionKnowledgeSearch), Description: "查询企业知识库并基于来源回答"},
		{Name: string(agent.ActionReject), Description: "请求高风险操作、敏感数据外发、绕过权限或不适合当前系统处理时拒答"},
	}
	if s.canUseWebSearch() {
		tools = append(tools, agent.ToolSpec{Name: string(agent.ActionWebSearch), Description: "查询实时或公开网络信息"})
	}
	return tools
}

func (s *AgentService) availableContextResolvedTools() []agent.ToolSpec {
	tools := s.availableFinalTools()
	return append([]agent.ToolSpec{{
		Name:        string(agent.ActionConversationAnswer),
		Description: "仅对会话中已有回答进行复述、缩写、翻译、格式转换或比较，必须提供来源chat_log_id",
	}}, tools...)
}

// availableProbeResolvedTools 返回知识库探测后的最终动作列表
func (s *AgentService) availableProbeResolvedTools() []agent.ToolSpec {
	tools := []agent.ToolSpec{
		{Name: string(agent.ActionDirectAnswer), Description: "知识库无明显命中且问题属于通用知识或写作任务时直接回答"},
		{Name: string(agent.ActionClarify), Description: "知识库无明显命中但问题仍像企业制度、流程或业务对象时追问用户"},
		{Name: string(agent.ActionKnowledgeSearch), Description: "探测命中相关企业文档时进入完整知识库检索"},
		{Name: string(agent.ActionReject), Description: "请求高风险操作、敏感数据外发、绕过权限或不适合当前系统处理时拒答"},
	}
	if s.canUseWebSearch() {
		tools = append(tools, agent.ToolSpec{Name: string(agent.ActionWebSearch), Description: "问题需要实时或公开网络信息时联网搜索"})
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

func uniqueUint64(values []uint64, limit int) []uint64 {
	seen := make(map[uint64]struct{}, len(values))
	result := make([]uint64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func dedupeRAGSources(sources []rag.Source) []rag.Source {
	seen := make(map[string]struct{}, len(sources))
	result := make([]rag.Source, 0, len(sources))
	for _, source := range sources {
		key := source.SourceType + "|" + uintString(source.DocumentID) + "|" + uintString(source.ChunkID) + "|" + source.URL
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, source)
	}
	return result
}

func uintString(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func buildDirectAnswerPrompt(question string) string {
	return "用户问题：\n" + strings.TrimSpace(question)
}

// saveAgentResponse保存Agent回答并组装响应
func (s *AgentService) saveAgentResponse(prepared *rag.PreparedChat, trace agent.Trace, start time.Time) (*AgentChatResponse, error) {
	logID, err := s.chat.saveLogWithAgent(prepared, start, trace, trace.FinalMode)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "保存Agent问答日志失败")
	}
	return &AgentChatResponse{
		ConversationID: prepared.Request.ConversationID,
		Answer:         prepared.Answer,
		Sources:        prepared.Sources,
		Trace:          prepared.Trace,
		AgentTrace:     trace,
		ChatLogID:      logID,
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
