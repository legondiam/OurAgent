package agent

import (
	"context"
	"strings"
	"unicode"

	"OurAgent/internal/rag"
)

type Planner interface {
	Plan(ctx context.Context, input PlannerInput) (Decision, error)
}

type PlannerStage string

const (
	PlannerStagePreRAG          PlannerStage = "pre_rag"
	PlannerStageContextResolved PlannerStage = "context_resolved"
	PlannerStageProbeResolved   PlannerStage = "probe_resolved"
	PlannerStagePostRAG         PlannerStage = "post_rag"
)

type PlannerInput struct {
	Stage         PlannerStage
	UserQuestion  string
	Tools         []ToolSpec
	WebEnabled    bool
	Observation   *RetrievalObservation
	Context       *ConversationContext
	ProbeResult   *KnowledgeProbeResult
	ProbeEvidence *ProbeEvidence
}

type ToolSpec struct {
	Name        string
	Description string
}

type RetrievalObservation struct {
	SearchQuery      string
	RewrittenQueries []string
	UsedChunkCount   int
	RejectReason     string
	TopHits          []RetrievalHitSummary
}

type RetrievalHitSummary struct {
	DocumentName string
	SectionPath  string
	Score        float64
	Used         bool
	Reason       string
}

type ConversationContext struct {
	ConversationID string
	Messages       []HistoryMessage
}

type HistoryMessage struct {
	Question string
	Answer   string
}

type KnowledgeProbeResult struct {
	Query    string
	Hits     []KnowledgeProbeHit
	MaxScore float64
}

type KnowledgeProbeHit struct {
	DocumentName   string
	SectionPath    string
	Score          float64
	ContentPreview string
}

type ProbeEvidence struct {
	Level   string   `json:"level"`
	Reasons []string `json:"reasons"`
}

const (
	ProbeEvidenceStrong = "strong"
	ProbeEvidenceWeak   = "weak"
	ProbeEvidenceNone   = "none"
)

type PostRAGPlanner struct{}

type FallbackPlanner struct{}

type ClarifyDecision struct {
	NeedClarify bool
	Question    string
	Reason      string
}

// NewPostRAGPlanner创建RAG后置规划器
func NewPostRAGPlanner() *PostRAGPlanner {
	return &PostRAGPlanner{}
}

// NewFallbackPlanner创建知识库检索兜底Planner
func NewFallbackPlanner() *FallbackPlanner {
	return &FallbackPlanner{}
}

// Plan返回默认知识库检索决策
func (p *FallbackPlanner) Plan(_ context.Context, input PlannerInput) (Decision, error) {
	if input.Stage == PlannerStagePostRAG {
		return DefaultPostRAGDecision(input.UserQuestion, input.WebEnabled), nil
	}
	return DefaultKnowledgeSearchDecision(input.UserQuestion, SearchPlan{}), nil
}

// ClarifyAfterRAG在RAG低置信度后判断是否追问
func (p *PostRAGPlanner) ClarifyAfterRAG(question string, trace rag.RetrievalTrace) ClarifyDecision {
	if trace.UsedChunkCount > 0 {
		return ClarifyDecision{}
	}
	if !hasVagueReference(question) {
		return ClarifyDecision{}
	}
	if hasSpecificObject(question) || rewrittenQueriesHaveObject(trace.RewrittenQueries) {
		return ClarifyDecision{}
	}
	return ClarifyDecision{
		NeedClarify: true,
		Question:    "为了准确查询知识库，请补充你想查询的具体流程、制度或文档范围。",
		Reason:      "问题缺少明确业务对象，query rewrite后仍未检索到可靠内容",
	}
}

// rewrittenQueriesHaveObject判断改写query是否包含业务对象
func rewrittenQueriesHaveObject(queries []rag.TraceQuery) bool {
	for _, query := range queries {
		if hasSpecificObject(query.Query) {
			return true
		}
	}
	return false
}

// hasVagueReference判断问题是否存在泛化指代
func hasVagueReference(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, token := range []string{
		"这个", "那个", "这些", "那些", "它", "其", "该",
		"最新政策", "相关政策", "这个流程", "这个制度", "这个文档",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return runeLen(text) <= 8 && containsAny(text, []string{"流程", "政策", "制度", "要求", "限制"})
}

// hasSpecificObject判断文本是否留下明确业务对象
func hasSpecificObject(text string) bool {
	cleaned := removePunctuation(text)
	for _, token := range []string{
		"这个", "那个", "这些", "那些", "它", "其", "该",
		"最新", "相关", "流程", "政策", "制度", "文档", "内容",
		"怎么", "如何", "什么", "哪些", "是否", "可以", "需要",
		"要求", "限制", "步骤", "办理", "申请", "总结", "一下",
		"查询", "查看", "介绍", "说明", "规定",
	} {
		cleaned = strings.ReplaceAll(cleaned, token, "")
	}
	return runeLen(cleaned) >= 2
}

// removePunctuation去掉空白和标点符号
func removePunctuation(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// containsAny判断文本是否包含任一关键词
func containsAny(text string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

// runeLen返回文本字符数量
func runeLen(text string) int {
	return len([]rune(strings.TrimSpace(text)))
}
