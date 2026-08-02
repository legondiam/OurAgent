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
	Reason           string          `json:"reason"`
	SearchPlan       json.RawMessage `json:"search_plan,omitempty"`
	ClarifyQuestion  *string         `json:"clarify_question,omitempty"`
	SourceChatLogIDs []uint64        `json:"source_chat_log_ids,omitempty"`
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
		Action:           action,
		Reason:           args.Reason,
		SourceChatLogIDs: args.SourceChatLogIDs,
	}
	switch action {
	case ActionKnowledgeProbe, ActionKnowledgeSearch:
		searchPlan, err := parseSearchPlanArg(args.SearchPlan)
		if err != nil {
			return Decision{}, fmt.Errorf("%s的search_plan无效：%w", action, err)
		}
		if strings.TrimSpace(searchPlan.Query) == "" {
			return Decision{}, fmt.Errorf("%s缺少search_plan", action)
		}
		decision.SearchPlan = searchPlan
	case ActionClarify:
		if args.ClarifyQuestion == nil || strings.TrimSpace(*args.ClarifyQuestion) == "" {
			return Decision{}, fmt.Errorf("%s缺少clarify_question", action)
		}
		decision.ClarifyQuestion = *args.ClarifyQuestion
	case ActionConversationAnswer:
		if !hasValidSourceChatLogID(args.SourceChatLogIDs) {
			return Decision{}, fmt.Errorf("%s缺少source_chat_log_ids", action)
		}
	case ActionContextLookup, ActionDirectAnswer, ActionWebSearch, ActionReject:
	default:
		return Decision{}, fmt.Errorf("不支持function：%s", action)
	}
	return decision, nil
}

func hasValidSourceChatLogID(ids []uint64) bool {
	for _, id := range ids {
		if id != 0 {
			return true
		}
	}
	return false
}

func parseSearchPlanArg(raw json.RawMessage) (SearchPlan, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return SearchPlan{}, fmt.Errorf("缺少search_plan")
	}
	var plan SearchPlan
	if err := json.Unmarshal(raw, &plan); err == nil {
		return plan, nil
	}
	var query string
	if err := json.Unmarshal(raw, &query); err != nil {
		return SearchPlan{}, err
	}
	return SearchPlan{Query: strings.TrimSpace(query)}, nil
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
	case ActionConversationAnswer:
		return &schema.ToolInfo{
			Name: string(action),
			Desc: desc,
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"reason": requiredStringParam("选择该动作的原因"),
				"source_chat_log_ids": {
					Type:     schema.Array,
					Desc:     "需要复述或转换的会话日志ID",
					Required: true,
					ElemInfo: &schema.ParameterInfo{Type: schema.Integer},
				},
			}),
		}
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
		b.WriteString("probe有命中不等于可以进入完整RAG。只有命中文档明确覆盖用户问题中的关键对象、版本、产品、政策范围和问题主题时，才选择knowledge_search。\n")
		b.WriteString("如果probe命中只是相似主题或泛化概念，不要默认knowledge_search。\n")
		b.WriteString("如果用户问题中的关键产品、版本、系统、地区或对象没有被命中文档明确覆盖，优先clarify。\n")
		b.WriteString("但如果用户问题没有陌生产品名、陌生系统名或未知版本，且probe命中了明确企业制度、产品套餐、权限、安全、入职、报销、私有化、字段限制、同步失败等主题，即使服务端Probe证据判断为weak，也可以选择knowledge_search。\n")
		b.WriteString("如果服务端Probe证据判断为weak或none，且用户问题带有具体产品、系统、平台、连接器、版本、客户范围或内部业务对象，优先clarify，不要因为问题也像通用解释、建议、示例、话术或设计维度就direct_answer。\n")
		b.WriteString("只有在用户问题没有具体企业产品、系统、平台、连接器、版本或内部业务对象，且只是通用解释、建议、示例、话术、设计维度时，才优先direct_answer。\n")
		b.WriteString("如果用户请求执行绕过审批、越权、伪造、隐藏审计、私下处理客户数据等高风险动作，优先reject。\n")
		b.WriteString("如果用户问题依赖实时公开信息、官方状态、最新公告、价格或API变化，且probe没有明确内部资料，优先web_search。\n")
		b.WriteString("如果服务端Probe证据判断为weak或none，不要仅因为有相似文档命中就选择knowledge_search。\n\n")
	} else if input.Stage == PlannerStageContextResolved {
		b.WriteString("已经自动读取会话历史。当前问题优先于历史，只能使用相关话题，不要把其他话题的实体补入当前问题。\n")
		b.WriteString("如果用户只是复述、缩写、翻译、格式转换或比较既有回答，选择conversation_answer并给出source_chat_log_ids。\n")
		b.WriteString("如果用户提出版本、有效性、适用范围、例外或其他新的企业事实判断，必须选择knowledge_probe或knowledge_search，不要选择conversation_answer。\n")
		b.WriteString("你可以选择conversation_answer、knowledge_probe、direct_answer、clarify、knowledge_search、web_search或reject，不要选择context_lookup。\n")
		b.WriteString("如果选择knowledge_search，search_plan.query必须是结合会话历史后的独立完整问题。\n\n")
	} else {
		b.WriteString("你可以选择context_lookup、knowledge_probe、direct_answer、clarify、knowledge_search、web_search或reject。\n")
		b.WriteString("如果当前问题明显依赖上文，且context_lookup可用，优先选择context_lookup。\n\n")
	}
	b.WriteString("reject优先级最高。用户请求实际删除或篡改审计日志、导出或发送客户数据到个人邮箱、绕过权限、泄露密钥、处理敏感数据等高风险操作时，直接选择reject，不要knowledge_probe、knowledge_search或clarify。\n")
	b.WriteString("如果用户只是询问客户数据、审计日志、权限或安全规范是否允许、有哪些限制、影响或合规要求，应该选择knowledge_probe或knowledge_search，不要reject。\n")
	b.WriteString("包含“我能不能、能否、是否可以、可不可以”的问题通常是在询问合规边界，即使动作本身高风险，也应查询知识库给出依据；包含“帮我、请你、替我”并要求执行高风险动作时才reject。\n")
	b.WriteString("direct_answer仅用于寒暄、通用知识解释、写作辅助、格式转换等不依赖企业知识库和实时网络信息的问题。\n")
	b.WriteString("如果用户只是要求给出通用建议、示例、维度或写作素材，且没有要求依据公司制度、产品手册或内部标准，选择direct_answer。\n")
	b.WriteString("如果问题涉及企业内部制度、流程、文档、配置、权限、数据或需要来源依据，不要选择direct_answer。\n")
	b.WriteString("如果问题涉及实时公开信息，不要选择direct_answer，应选择web_search。\n\n")
	b.WriteString("knowledge_probe用于看似通用但可能包含企业制度、流程、报销、发票、审批、权限、配置、产品、套餐、项目、价格、规格、外部知识源、同步失败、索引失败等业务线索的问题，用来轻量探测知识库是否有相关资料。\n")
	b.WriteString("knowledge_probe的search_plan.query应保留用户关键词，并补充可能的企业文档上位词或同义词，例如字段数量限制可补充产品、套餐、字段上限、扩容申请。\n")
	b.WriteString("如果问题有可检索的企业业务线索，不要仅因为用户没说公司名就clarify，优先knowledge_probe或knowledge_search。\n")
	b.WriteString("clarify只用于缺少可检索对象、指代严重依赖上文或必须由用户补充范围的问题。\n")
	b.WriteString("明确企业制度、流程、文档、项目或产品资料时，直接选择knowledge_search；不确定知识库是否覆盖时，选择knowledge_probe；明确实时公开信息时，选择web_search。\n\n")
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
	if input.ProbeEvidence != nil {
		writeProbeEvidence(&b, input.ProbeEvidence)
	}
	if input.ProbeResult != nil {
		writeKnowledgeProbeResult(&b, input.ProbeResult)
	}
	return b.String()
}

func writeProbeEvidence(b *strings.Builder, evidence *ProbeEvidence) {
	b.WriteString("\n\n服务端Probe证据判断：\n")
	b.WriteString("- level：")
	b.WriteString(evidence.Level)
	b.WriteString("\n- reasons：")
	if len(evidence.Reasons) == 0 {
		b.WriteString("[]\n")
		return
	}
	b.WriteString("\n")
	for _, reason := range evidence.Reasons {
		b.WriteString("  - ")
		b.WriteString(reason)
		b.WriteString("\n")
	}
}

func writeConversationContext(b *strings.Builder, context *ConversationContext) {
	b.WriteString("\n\n会话历史：\n")
	if strings.TrimSpace(context.Summary) != "" {
		b.WriteString("会话摘要：\n")
		b.WriteString(context.Summary)
		b.WriteString("\n\n摘要后的原始问答：\n")
	}
	if len(context.Messages) == 0 {
		b.WriteString("[]\n")
		return
	}
	for i, message := range context.Messages {
		b.WriteString(intString(i + 1))
		b.WriteString(". chat_log_id=")
		b.WriteString(strconv.FormatUint(message.ChatLogID, 10))
		b.WriteString("\n   用户：")
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
