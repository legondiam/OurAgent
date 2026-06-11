package agent

import "strings"

type Action string

const (
	ActionContextLookup   Action = "context_lookup"
	ActionClarify         Action = "clarify"
	ActionKnowledgeSearch Action = "knowledge_search"
	ActionWebSearch       Action = "web_search"
	ActionReject          Action = "reject"
)

type Decision struct {
	Action          Action     `json:"action"`
	Reason          string     `json:"reason"`
	SearchPlan      SearchPlan `json:"search_plan,omitempty"`
	ClarifyQuestion string     `json:"clarify_question,omitempty"`
}

type SearchPlan struct {
	Query               string `json:"query"`
	TopK                int    `json:"top_k"`
	QueryRewriteEnabled bool   `json:"query_rewrite_enabled"`
	HybridEnabled       bool   `json:"hybrid_enabled"`
	RerankEnabled       bool   `json:"rerank_enabled"`
	Reason              string `json:"reason,omitempty"`
}

const defaultClarifyQuestion = "请补充你想查询的具体流程、制度或文档范围。"

// DefaultKnowledgeSearchDecision创建知识库检索兜底决策
func DefaultKnowledgeSearchDecision(question string, defaults SearchPlan) Decision {
	if defaults.Query == "" {
		defaults.Query = strings.TrimSpace(question)
	}
	return Decision{
		Action:     ActionKnowledgeSearch,
		Reason:     "Planner不可用，默认查询知识库",
		SearchPlan: defaults,
	}
}

// DefaultPostRAGDecision创建低置信度后的兜底决策
func DefaultPostRAGDecision(_ string, webEnabled bool) Decision {
	if webEnabled {
		return Decision{
			Action: ActionWebSearch,
			Reason: "知识库检索低置信度，默认尝试联网补充",
		}
	}
	return Decision{
		Action: ActionReject,
		Reason: "知识库检索低置信度且联网搜索不可用",
	}
}

// NormalizeDecision校验并修正Planner决策
func NormalizeDecision(decision Decision, input PlannerInput, defaults SearchPlan) Decision {
	if input.Stage == "" {
		input.Stage = PlannerStagePreRAG
	}
	decision.Action = normalizeAction(decision.Action)
	if decision.Action == "" {
		decision.Action = defaultActionForStage(input)
	}
	if decision.Reason == "" {
		decision.Reason = "Planner未提供原因"
	}
	if input.Stage == PlannerStagePostRAG {
		return normalizePostRAGDecision(decision, input)
	}
	if input.Stage == PlannerStageContextResolved {
		return normalizeContextResolvedDecision(decision, input, defaults)
	}
	if decision.Action == ActionContextLookup && !hasTool(input.Tools, ActionContextLookup) {
		decision.Action = ActionClarify
		decision.Reason = "会话上下文不可用，改为澄清追问"
	}
	if decision.Action == ActionWebSearch && !input.WebEnabled {
		decision.Action = ActionKnowledgeSearch
		decision.Reason = "联网搜索未启用，改为查询知识库"
	}
	switch decision.Action {
	case ActionContextLookup:
		decision.SearchPlan = SearchPlan{}
	case ActionClarify:
		if strings.TrimSpace(decision.ClarifyQuestion) == "" {
			decision.ClarifyQuestion = defaultClarifyQuestion
		}
	case ActionKnowledgeSearch:
		decision.SearchPlan = NormalizeSearchPlan(decision.SearchPlan, input.UserQuestion, defaults)
	case ActionWebSearch, ActionReject:
		decision.SearchPlan = SearchPlan{}
	}
	return decision
}

func normalizeContextResolvedDecision(decision Decision, input PlannerInput, defaults SearchPlan) Decision {
	if decision.Action == "" || decision.Action == ActionContextLookup {
		decision.Action = ActionClarify
		decision.Reason = "已读取会话上下文，仍缺少明确问题，改为澄清追问"
	}
	if decision.Action == ActionWebSearch && !input.WebEnabled {
		decision.Action = ActionKnowledgeSearch
		decision.Reason = "联网搜索未启用，改为查询知识库"
	}
	switch decision.Action {
	case ActionClarify:
		if strings.TrimSpace(decision.ClarifyQuestion) == "" {
			decision.ClarifyQuestion = defaultClarifyQuestion
		}
	case ActionKnowledgeSearch:
		decision.SearchPlan = NormalizeSearchPlan(decision.SearchPlan, input.UserQuestion, defaults)
	case ActionWebSearch, ActionReject:
		decision.SearchPlan = SearchPlan{}
	default:
		decision.Action = ActionClarify
		decision.Reason = "会话上下文后二次规划动作非法，改为澄清追问"
		if strings.TrimSpace(decision.ClarifyQuestion) == "" {
			decision.ClarifyQuestion = defaultClarifyQuestion
		}
		decision.SearchPlan = SearchPlan{}
	}
	return decision
}

func normalizePostRAGDecision(decision Decision, input PlannerInput) Decision {
	if decision.Action == ActionContextLookup {
		decision.Action = ActionClarify
		decision.Reason = "后置规划阶段不能读取会话上下文，改为澄清追问"
	}
	if decision.Action == ActionKnowledgeSearch || decision.Action == "" {
		decision = DefaultPostRAGDecision(input.UserQuestion, input.WebEnabled)
	}
	if decision.Action == ActionWebSearch && !input.WebEnabled {
		decision.Action = ActionReject
		decision.Reason = "联网搜索未启用，改为拒答"
	}
	switch decision.Action {
	case ActionClarify:
		if strings.TrimSpace(decision.ClarifyQuestion) == "" {
			decision.ClarifyQuestion = defaultClarifyQuestion
		}
	case ActionWebSearch, ActionReject:
		decision.SearchPlan = SearchPlan{}
	default:
		decision = DefaultPostRAGDecision(input.UserQuestion, input.WebEnabled)
	}
	if decision.Reason == "" {
		decision.Reason = "低置信度后置决策"
	}
	return decision
}

func defaultActionForStage(input PlannerInput) Action {
	if input.Stage == PlannerStagePostRAG {
		if input.WebEnabled {
			return ActionWebSearch
		}
		return ActionReject
	}
	if input.Stage == PlannerStageContextResolved {
		return ActionClarify
	}
	return ActionKnowledgeSearch
}

// NormalizeSearchPlan校验并修正检索计划
func NormalizeSearchPlan(plan SearchPlan, question string, defaults SearchPlan) SearchPlan {
	if isZeroSearchPlan(plan) {
		plan = defaults
	}
	if strings.TrimSpace(plan.Query) == "" {
		plan.Query = strings.TrimSpace(question)
	}
	if plan.TopK <= 0 {
		plan.TopK = defaults.TopK
	}
	if plan.TopK <= 0 {
		plan.TopK = 5
	}
	if plan.TopK > 20 {
		plan.TopK = 20
	}
	if strings.TrimSpace(plan.Reason) == "" {
		plan.Reason = "使用Planner生成的检索计划"
	}
	return plan
}

func normalizeAction(action Action) Action {
	switch Action(strings.TrimSpace(string(action))) {
	case ActionContextLookup:
		return ActionContextLookup
	case ActionClarify:
		return ActionClarify
	case ActionKnowledgeSearch:
		return ActionKnowledgeSearch
	case ActionWebSearch:
		return ActionWebSearch
	case ActionReject:
		return ActionReject
	default:
		return ""
	}
}

func hasTool(tools []ToolSpec, action Action) bool {
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == string(action) {
			return true
		}
	}
	return false
}

func isZeroSearchPlan(plan SearchPlan) bool {
	return strings.TrimSpace(plan.Query) == "" &&
		plan.TopK == 0 &&
		!plan.QueryRewriteEnabled &&
		!plan.HybridEnabled &&
		!plan.RerankEnabled &&
		strings.TrimSpace(plan.Reason) == ""
}
