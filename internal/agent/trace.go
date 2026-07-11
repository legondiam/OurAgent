package agent

import "strings"

const (
	IntentKnowledgeQA     = "knowledge_qa"
	IntentWebAugmentedQA  = "web_augmented_qa"
	IntentUnclearQuestion = "unclear_question"

	FinalModeKnowledgeBase = "knowledge_base"
	FinalModeWebFallback   = "web_fallback"
	FinalModeDirectAnswer  = "direct_answer"
	FinalModeClarify       = "clarify"
	FinalModeRejected      = "rejected"

	ToolKnowledgeSearch = "knowledge_search"
	ToolWebSearch       = "web_search"
	ToolAgentPlanner    = "agent_planner"
	ToolContextLookup   = "context_lookup"
	ToolKnowledgeProbe  = "knowledge_probe"
	ToolDirectAnswer    = "direct_answer"

	StatusSuccess       = "success"
	StatusLowConfidence = "low_confidence"
	StatusError         = "error"
	StatusSkipped       = "skipped"
)

type Trace struct {
	Intent          string         `json:"intent"`
	Plan            string         `json:"plan"`
	ConversationID  string         `json:"conversation_id,omitempty"`
	Steps           []Step         `json:"steps"`
	FinalMode       string         `json:"final_mode"`
	Clarify         string         `json:"clarify,omitempty"`
	RejectReason    string         `json:"reject_reason,omitempty"`
	PlannerDecision *Decision      `json:"planner_decision,omitempty"`
	PreRAGDecision  *Decision      `json:"pre_rag_decision,omitempty"`
	ContextDecision *Decision      `json:"context_decision,omitempty"`
	ProbeDecision   *Decision      `json:"probe_decision,omitempty"`
	ProbeEvidence   *ProbeEvidence `json:"probe_evidence,omitempty"`
	PostRAGDecision *Decision      `json:"post_rag_decision,omitempty"`
	PlannerError    string         `json:"planner_error,omitempty"`
}

type Step struct {
	Tool     string         `json:"tool"`
	Action   string         `json:"action"`
	Status   string         `json:"status"`
	Reason   string         `json:"reason,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// NewTrace创建Agent执行轨迹
func NewTrace(intent string) Trace {
	return Trace{
		Intent: intent,
		Plan:   "先由Agent Router决策，知识库低置信度时再进行后置决策",
		Steps:  []Step{},
	}
}

// AddStep追加工具执行步骤
func (t *Trace) AddStep(step Step) {
	t.Steps = append(t.Steps, step)
}

// MarkPlannerDecision记录Planner结构化决策
func (t *Trace) MarkPlannerDecision(decision Decision) {
	t.PlannerDecision = &decision
	t.PreRAGDecision = &decision
	t.AddStep(Step{
		Tool:   ToolAgentPlanner,
		Action: "pre_rag_plan",
		Status: StatusSuccess,
		Reason: decision.Reason,
		Metadata: map[string]any{
			"decision_action": string(decision.Action),
			"planned_query":   decision.SearchPlan.Query,
		},
	})
}

// MarkPostRAGDecision记录低置信度后的Planner决策
func (t *Trace) MarkPostRAGDecision(decision Decision) {
	t.PostRAGDecision = &decision
	t.AddStep(Step{
		Tool:   ToolAgentPlanner,
		Action: "post_rag_plan",
		Status: StatusSuccess,
		Reason: decision.Reason,
		Metadata: map[string]any{
			"decision_action": string(decision.Action),
		},
	})
}

// MarkContextResolvedDecision记录读取上下文后的Planner决策
func (t *Trace) MarkContextResolvedDecision(decision Decision) {
	t.ContextDecision = &decision
	t.AddStep(Step{
		Tool:   ToolAgentPlanner,
		Action: "context_resolved_plan",
		Status: StatusSuccess,
		Reason: decision.Reason,
		Metadata: map[string]any{
			"decision_action": string(decision.Action),
			"planned_query":   decision.SearchPlan.Query,
		},
	})
}

// MarkProbeResolvedDecision 记录知识库探测后的Planner决策
func (t *Trace) MarkProbeResolvedDecision(decision Decision) {
	t.ProbeDecision = &decision
	metadata := map[string]any{
		"decision_action": string(decision.Action),
		"planned_query":   decision.SearchPlan.Query,
	}
	if t.ProbeEvidence != nil {
		metadata["probe_evidence_level"] = t.ProbeEvidence.Level
		metadata["probe_evidence_reasons"] = t.ProbeEvidence.Reasons
	}
	t.AddStep(Step{
		Tool:     ToolAgentPlanner,
		Action:   "probe_resolved_plan",
		Status:   StatusSuccess,
		Reason:   decision.Reason,
		Metadata: metadata,
	})
}

// MarkProbeEvidence记录轻量探测证据强弱
func (t *Trace) MarkProbeEvidence(evidence ProbeEvidence) {
	t.ProbeEvidence = &evidence
	t.AddStep(Step{
		Tool:   ToolKnowledgeProbe,
		Action: "evidence",
		Status: StatusSuccess,
		Reason: strings.Join(evidence.Reasons, "；"),
		Metadata: map[string]any{
			"level": evidence.Level,
		},
	})
}

// MarkContextLookup记录会话上下文工具调用
func (t *Trace) MarkContextLookup(conversationID string, historyCount int, err error) {
	status := StatusSuccess
	reason := ""
	if err != nil {
		status = StatusError
		reason = err.Error()
	} else if historyCount == 0 {
		status = StatusSkipped
		reason = "没有可用会话历史"
	}
	t.AddStep(Step{
		Tool:   ToolContextLookup,
		Action: "lookup",
		Status: status,
		Reason: reason,
		Metadata: map[string]any{
			"conversation_id": conversationID,
			"history_count":   historyCount,
		},
	})
}

// MarkPlannerError记录Planner失败原因
func (t *Trace) MarkPlannerError(err error) {
	if err == nil {
		return
	}
	t.PlannerError = err.Error()
	t.AddStep(Step{
		Tool:   ToolAgentPlanner,
		Action: "plan_error",
		Status: StatusError,
		Reason: err.Error(),
	})
}

// MarkClarify标记最终结果为澄清追问
func (t *Trace) MarkClarify(question string) {
	t.Intent = IntentUnclearQuestion
	t.FinalMode = FinalModeClarify
	t.Clarify = question
}

// MarkRejected标记最终结果为拒答
func (t *Trace) MarkRejected(reason string) {
	t.FinalMode = FinalModeRejected
	t.RejectReason = reason
}
