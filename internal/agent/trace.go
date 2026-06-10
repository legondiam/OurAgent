package agent

const (
	IntentKnowledgeQA     = "knowledge_qa"
	IntentWebAugmentedQA  = "web_augmented_qa"
	IntentUnclearQuestion = "unclear_question"

	FinalModeKnowledgeBase = "knowledge_base"
	FinalModeWebFallback   = "web_fallback"
	FinalModeClarify       = "clarify"
	FinalModeRejected      = "rejected"

	ToolKnowledgeSearch = "knowledge_search"
	ToolWebSearch       = "web_search"

	StatusSuccess       = "success"
	StatusLowConfidence = "low_confidence"
	StatusError         = "error"
	StatusSkipped       = "skipped"
)

type Trace struct {
	Intent       string `json:"intent"`
	Plan         string `json:"plan"`
	Steps        []Step `json:"steps"`
	FinalMode    string `json:"final_mode"`
	Clarify      string `json:"clarify,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`
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
		Plan:   "先查询知识库，低置信度时再决定澄清或联网补充",
		Steps:  []Step{},
	}
}

// AddStep追加工具执行步骤
func (t *Trace) AddStep(step Step) {
	t.Steps = append(t.Steps, step)
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
