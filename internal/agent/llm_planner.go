package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	pkgerrors "github.com/pkg/errors"
)

type LLMPlanner struct {
	chat einomodel.BaseChatModel
}

// NewLLMPlanner创建基于EinoChatModel的Planner
func NewLLMPlanner(chat einomodel.BaseChatModel) *LLMPlanner {
	return &LLMPlanner{chat: chat}
}

// Plan调用LLM生成AgentRouter决策
func (p *LLMPlanner) Plan(ctx context.Context, input PlannerInput) (Decision, error) {
	messages := []*schema.Message{
		schema.SystemMessage(agentRouterSystemPrompt),
		schema.UserMessage(buildPlannerPrompt(input)),
	}
	tools := buildPlannerToolInfos(input)
	resp, err := p.chat.Generate(
		ctx,
		messages,
		einomodel.WithTools(tools),
		einomodel.WithToolChoice(schema.ToolChoiceForced),
	)
	if err != nil {
		return Decision{}, pkgerrors.WithMessage(err, "调用Agent Planner失败")
	}
	decision, err := ParseToolCallDecision(resp.ToolCalls, input)
	if err != nil {
		return Decision{}, pkgerrors.WithMessage(err, "解析Agent Planner工具调用失败")
	}
	return decision, nil
}

type plannerToolArgs struct {
	Reason          string      `json:"reason"`
	SearchPlan      *SearchPlan `json:"search_plan,omitempty"`
	ClarifyQuestion *string     `json:"clarify_question,omitempty"`
}

// ParseToolCallDecision解析Planner工具调用
func ParseToolCallDecision(calls []schema.ToolCall, input PlannerInput) (Decision, error) {
	if len(calls) != 1 {
		return Decision{}, fmt.Errorf("期望1个tool_call，实际%d个", len(calls))
	}
	call := calls[0]
	action := normalizeAction(Action(call.Function.Name))
	if action == "" {
		return Decision{}, fmt.Errorf("未知function：%s", call.Function.Name)
	}
	if !hasTool(input.Tools, action) {
		return Decision{}, fmt.Errorf("当前阶段不允许function：%s", call.Function.Name)
	}
	var args plannerToolArgs
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return Decision{}, err
	}
	if strings.TrimSpace(args.Reason) == "" {
		return Decision{}, fmt.Errorf("%s缺少reason", action)
	}
	decision := Decision{
		Action: action,
		Reason: args.Reason,
	}
	switch action {
	case ActionKnowledgeProbe, ActionKnowledgeSearch:
		if args.SearchPlan == nil {
			return Decision{}, fmt.Errorf("%s缺少search_plan", action)
		}
		decision.SearchPlan = *args.SearchPlan
	case ActionClarify:
		if args.ClarifyQuestion == nil || strings.TrimSpace(*args.ClarifyQuestion) == "" {
			return Decision{}, fmt.Errorf("%s缺少clarify_question", action)
		}
		decision.ClarifyQuestion = *args.ClarifyQuestion
	case ActionContextLookup, ActionDirectAnswer, ActionWebSearch, ActionReject:
	default:
		return Decision{}, fmt.Errorf("不支持function：%s", action)
	}
	return decision, nil
}

func buildPlannerToolInfos(input PlannerInput) []*schema.ToolInfo {
	tools := make([]*schema.ToolInfo, 0, len(input.Tools))
	for _, tool := range input.Tools {
		action := normalizeAction(Action(tool.Name))
		if action == "" {
			continue
		}
		info := plannerToolInfo(action, tool.Description)
		if info != nil {
			tools = append(tools, info)
		}
	}
	return tools
}

func plannerToolInfo(action Action, desc string) *schema.ToolInfo {
	switch action {
	case ActionContextLookup:
		return reasonOnlyTool(action, desc)
	case ActionKnowledgeProbe:
		return &schema.ToolInfo{
			Name: string(action),
			Desc: desc,
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"reason": requiredStringParam("选择该动作的原因"),
				"search_plan": {
					Type:     schema.Object,
					Desc:     "轻量探测计划",
					Required: true,
					SubParams: map[string]*schema.ParameterInfo{
						"query":  requiredStringParam("用于轻量探测知识库的查询"),
						"reason": optionalStringParam("探测query的原因"),
					},
				},
			}),
		}
	case ActionKnowledgeSearch:
		return &schema.ToolInfo{
			Name: string(action),
			Desc: desc,
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"reason":      requiredStringParam("选择该动作的原因"),
				"search_plan": requiredSearchPlanParam(),
			}),
		}
	case ActionDirectAnswer:
		return reasonOnlyTool(action, desc)
	case ActionClarify:
		return &schema.ToolInfo{
			Name: string(action),
			Desc: desc,
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"reason":           requiredStringParam("选择该动作的原因"),
				"clarify_question": requiredStringParam("需要追问用户的问题"),
			}),
		}
	case ActionWebSearch:
		return reasonOnlyTool(action, desc)
	case ActionReject:
		return reasonOnlyTool(action, desc)
	default:
		return nil
	}
}

func reasonOnlyTool(action Action, desc string) *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: string(action),
		Desc: desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"reason": requiredStringParam("选择该动作的原因"),
		}),
	}
}

func requiredSearchPlanParam() *schema.ParameterInfo {
	return &schema.ParameterInfo{
		Type:     schema.Object,
		Desc:     "知识库检索计划",
		Required: true,
		SubParams: map[string]*schema.ParameterInfo{
			"query":                 requiredStringParam("用于完整知识库检索的独立完整问题"),
			"top_k":                 optionalIntegerParam("检索候选数量"),
			"query_rewrite_enabled": optionalBooleanParam("是否启用query改写"),
			"hybrid_enabled":        optionalBooleanParam("是否启用混合检索"),
			"rerank_enabled":        optionalBooleanParam("是否启用重排"),
			"reason":                optionalStringParam("检索计划原因"),
		},
	}
}

func requiredStringParam(desc string) *schema.ParameterInfo {
	return &schema.ParameterInfo{Type: schema.String, Desc: desc, Required: true}
}

func optionalStringParam(desc string) *schema.ParameterInfo {
	return &schema.ParameterInfo{Type: schema.String, Desc: desc}
}

func optionalIntegerParam(desc string) *schema.ParameterInfo {
	return &schema.ParameterInfo{Type: schema.Integer, Desc: desc}
}

func optionalBooleanParam(desc string) *schema.ParameterInfo {
	return &schema.ParameterInfo{Type: schema.Boolean, Desc: desc}
}

// ParseDecision解析Planner输出JSON
func ParseDecision(content string) (Decision, error) {
	content = extractJSONObject(content)
	var decision Decision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func extractJSONObject(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		content = content[start : end+1]
	}
	return strings.TrimSpace(content)
}

const agentRouterSystemPrompt = `你是企业知识库Agent Router。
你的任务是根据当前阶段选择下一步动作。

要求：
1. 不要回答用户问题
2. 不要编造工具
3. 不要编造知识库内容
4. 只能调用当前阶段提供的function
5. 如果选择knowledge_search，必须给出search_plan
6. 如果选择clarify，必须给出clarify_question
7. 如果web_search不可用，不能选择web_search
8. 当前阶段未允许的function不能选择
9. 无法处理时调用reject`

func buildPlannerPrompt(input PlannerInput) string {
	if input.Stage == "" {
		input.Stage = PlannerStagePreRAG
	}
	var b strings.Builder
	b.WriteString("当前阶段：")
	b.WriteString(string(input.Stage))
	b.WriteString("\n")
	if input.Stage == PlannerStagePostRAG {
		b.WriteString("知识库已经检索过一次，但结果低置信度。你只能选择clarify、web_search或reject，不要选择knowledge_probe、knowledge_search或direct_answer。\n\n")
	} else if input.Stage == PlannerStageProbeResolved {
		b.WriteString("已经完成知识库轻量探测。你只能选择direct_answer、clarify、knowledge_search、web_search或reject，不要选择knowledge_probe或context_lookup。\n")
		b.WriteString("如果probe命中高相关企业文档，选择knowledge_search；如果probe无明显命中且问题明显通用，选择direct_answer；如果probe无明显命中但问题仍像业务对象，选择clarify或reject。\n\n")
	} else if input.Stage == PlannerStageContextResolved {
		b.WriteString("已经读取会话历史。你可以选择knowledge_probe、direct_answer、clarify、knowledge_search、web_search或reject，不要选择context_lookup。\n")
		b.WriteString("如果选择knowledge_search，search_plan.query必须是结合会话历史后的独立完整问题。\n\n")
	} else {
		b.WriteString("你可以选择context_lookup、knowledge_probe、direct_answer、clarify、knowledge_search、web_search或reject。\n")
		b.WriteString("如果当前问题明显依赖上文，且context_lookup可用，优先选择context_lookup。\n\n")
	}
	b.WriteString("direct_answer仅用于寒暄、通用知识解释、写作辅助、格式转换等不依赖企业知识库和实时网络信息的问题。\n")
	b.WriteString("如果问题涉及企业内部制度、流程、文档、配置、权限、数据或需要来源依据，不要选择direct_answer。\n")
	b.WriteString("如果问题涉及实时公开信息，不要选择direct_answer，应选择web_search。\n\n")
	b.WriteString("knowledge_probe用于看似通用但可能包含企业产品、型号、套餐、项目、价格、规格、产地等业务对象的问题，用来轻量探测知识库是否有相关资料。\n")
	b.WriteString("明确企业制度、流程、文档、项目或产品资料时，直接选择knowledge_search；明确实时公开信息时，选择web_search。\n\n")
	b.WriteString("用户问题：\n")
	b.WriteString(input.UserQuestion)
	b.WriteString("\n\n可用工具：\n")
	for _, tool := range input.Tools {
		b.WriteString("- ")
		b.WriteString(tool.Name)
		b.WriteString("：")
		b.WriteString(tool.Description)
		b.WriteString("\n")
	}
	b.WriteString("\nweb_search是否可用：")
	if input.WebEnabled {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	if input.Observation != nil {
		writeRetrievalObservation(&b, input.Observation)
	}
	if input.Context != nil {
		writeConversationContext(&b, input.Context)
	}
	if input.ProbeResult != nil {
		writeKnowledgeProbeResult(&b, input.ProbeResult)
	}
	return b.String()
}

func writeConversationContext(b *strings.Builder, context *ConversationContext) {
	b.WriteString("\n\n会话历史：\n")
	if len(context.Messages) == 0 {
		b.WriteString("[]\n")
		return
	}
	for i, message := range context.Messages {
		b.WriteString(intString(i + 1))
		b.WriteString(". 用户：")
		b.WriteString(message.Question)
		b.WriteString("\n   助手：")
		b.WriteString(message.Answer)
		b.WriteString("\n")
	}
}

func writeKnowledgeProbeResult(b *strings.Builder, result *KnowledgeProbeResult) {
	b.WriteString("\n\n知识库轻量探测结果：\n")
	b.WriteString("- probe_query：")
	b.WriteString(result.Query)
	b.WriteString("\n- max_score：")
	b.WriteString(floatString(result.MaxScore))
	b.WriteString("\n- top_hits：\n")
	if len(result.Hits) == 0 {
		b.WriteString("  []\n")
		return
	}
	for _, hit := range result.Hits {
		b.WriteString("  - document：")
		b.WriteString(hit.DocumentName)
		b.WriteString("；section：")
		b.WriteString(hit.SectionPath)
		b.WriteString("；score：")
		b.WriteString(floatString(hit.Score))
		if hit.ContentPreview != "" {
			b.WriteString("；preview：")
			b.WriteString(hit.ContentPreview)
		}
		b.WriteString("\n")
	}
}

func writeRetrievalObservation(b *strings.Builder, observation *RetrievalObservation) {
	b.WriteString("\n\n检索摘要：\n")
	b.WriteString("- search_query：")
	b.WriteString(observation.SearchQuery)
	b.WriteString("\n- used_chunk_count：")
	b.WriteString(intString(observation.UsedChunkCount))
	b.WriteString("\n- reject_reason：")
	b.WriteString(observation.RejectReason)
	b.WriteString("\n- rewritten_queries：")
	if len(observation.RewrittenQueries) == 0 {
		b.WriteString("[]")
	} else {
		for i, query := range observation.RewrittenQueries {
			if i > 0 {
				b.WriteString("；")
			}
			b.WriteString(query)
		}
	}
	b.WriteString("\n- top_hits：\n")
	if len(observation.TopHits) == 0 {
		b.WriteString("  []\n")
		return
	}
	for _, hit := range observation.TopHits {
		b.WriteString("  - document：")
		b.WriteString(hit.DocumentName)
		b.WriteString("；section：")
		b.WriteString(hit.SectionPath)
		b.WriteString("；score：")
		b.WriteString(floatString(hit.Score))
		b.WriteString("；used：")
		if hit.Used {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		if hit.Reason != "" {
			b.WriteString("；reason：")
			b.WriteString(hit.Reason)
		}
		b.WriteString("\n")
	}
}

func intString(v int) string {
	return strconv.Itoa(v)
}

func floatString(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
