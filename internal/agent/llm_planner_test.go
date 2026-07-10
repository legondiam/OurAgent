package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestParseDecisionFromMarkdownJSON(t *testing.T) {
	raw := "```json\n{\"action\":\"clarify\",\"reason\":\"缺少对象\",\"clarify_question\":\"请说明流程名称\"}\n```"

	decision, err := ParseDecision(raw)
	if err != nil {
		t.Fatalf("parse decision: %v", err)
	}
	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
	if decision.ClarifyQuestion == "" {
		t.Fatal("expected clarify question")
	}
}

func TestBuildPlannerPromptForPostRAG(t *testing.T) {
	prompt := buildPlannerPrompt(PlannerInput{
		Stage:        PlannerStagePostRAG,
		UserQuestion: "这个流程怎么走",
		WebEnabled:   true,
		Observation: &RetrievalObservation{
			SearchQuery:      "这个流程怎么走",
			RewrittenQueries: []string{"这个流程办理步骤"},
			UsedChunkCount:   0,
			RejectReason:     "没有达到置信度阈值的切片",
			TopHits: []RetrievalHitSummary{
				{DocumentName: "制度.md", SectionPath: "流程", Score: 0.31, Used: false, Reason: "低于相似度阈值"},
			},
		},
	})

	if !strings.Contains(prompt, "当前阶段：post_rag") {
		t.Fatal("expected post_rag stage")
	}
	if strings.Contains(prompt, "请输出JSON") {
		t.Fatal("function calling prompt should not ask for JSON output")
	}
	if strings.Contains(prompt, `"action"`) {
		t.Fatal("function calling prompt should not include action JSON schema")
	}
	if !strings.Contains(prompt, "没有达到置信度阈值的切片") {
		t.Fatal("expected retrieval observation")
	}
}

func TestBuildPlannerPromptForPreRAGContextLookup(t *testing.T) {
	prompt := buildPlannerPrompt(PlannerInput{
		Stage:        PlannerStagePreRAG,
		UserQuestion: "为什么要部门审批",
		Tools:        []ToolSpec{{Name: string(ActionContextLookup), Description: "读取会话历史"}},
	})

	if !strings.Contains(prompt, "当前阶段：pre_rag") {
		t.Fatal("expected pre_rag stage")
	}
	if !strings.Contains(prompt, "优先选择context_lookup") {
		t.Fatal("expected context lookup instruction")
	}
	if !strings.Contains(prompt, "direct_answer仅用于寒暄") {
		t.Fatal("expected direct answer instruction")
	}
	if !strings.Contains(prompt, "reject优先级最高") {
		t.Fatal("expected reject priority instruction")
	}
	if !strings.Contains(prompt, "实际删除或篡改审计日志") {
		t.Fatal("expected sensitive data rejection instruction")
	}
	if !strings.Contains(prompt, "只是询问客户数据、审计日志、权限或安全规范是否允许") {
		t.Fatal("expected safety policy lookup boundary instruction")
	}
	if !strings.Contains(prompt, "我能不能、能否、是否可以、可不可以") {
		t.Fatal("expected permission question lookup instruction")
	}
	if !strings.Contains(prompt, "帮我、请你、替我") {
		t.Fatal("expected high risk execution rejection instruction")
	}
	if !strings.Contains(prompt, "给出通用建议、示例、维度或写作素材") {
		t.Fatal("expected generic suggestion direct answer instruction")
	}
	if !strings.Contains(prompt, "不要选择direct_answer") {
		t.Fatal("expected direct answer safety boundary")
	}
	if !strings.Contains(prompt, "knowledge_probe用于看似通用但可能包含企业制度、流程、报销、发票、审批") {
		t.Fatal("expected knowledge probe instruction")
	}
	if !strings.Contains(prompt, "外部知识源、同步失败、索引失败") {
		t.Fatal("expected external source failure probe instruction")
	}
	if !strings.Contains(prompt, "补充可能的企业文档上位词或同义词") {
		t.Fatal("expected probe query expansion instruction")
	}
	if !strings.Contains(prompt, "字段数量限制可补充产品、套餐、字段上限、扩容申请") {
		t.Fatal("expected field limit probe query example")
	}
	if !strings.Contains(prompt, "不要仅因为用户没说公司名就clarify") {
		t.Fatal("expected gray enterprise probe instruction")
	}
	if !strings.Contains(prompt, "clarify只用于缺少可检索对象") {
		t.Fatal("expected clarify boundary instruction")
	}
	if strings.Contains(prompt, "请输出JSON") {
		t.Fatal("function calling prompt should not ask for JSON output")
	}
}

func TestBuildPlannerPromptForContextResolved(t *testing.T) {
	prompt := buildPlannerPrompt(PlannerInput{
		Stage:        PlannerStageContextResolved,
		UserQuestion: "为什么要部门审批",
		Context: &ConversationContext{
			ConversationID: "c1",
			Messages: []HistoryMessage{
				{Question: "报销流程怎么走", Answer: "报销流程包括提交单据、部门审批、财务复核"},
			},
		},
	})

	if !strings.Contains(prompt, "当前阶段：context_resolved") {
		t.Fatal("expected context_resolved stage")
	}
	if strings.Contains(prompt, `"action": "context_lookup`) {
		t.Fatal("context_resolved prompt should not ask for context_lookup")
	}
	if !strings.Contains(prompt, "报销流程怎么走") {
		t.Fatal("expected conversation history")
	}
	if !strings.Contains(prompt, "独立完整问题") {
		t.Fatal("expected standalone query instruction")
	}
	if strings.Contains(prompt, "请输出JSON") {
		t.Fatal("function calling prompt should not ask for JSON output")
	}
}

func TestBuildPlannerPromptForProbeResolved(t *testing.T) {
	prompt := buildPlannerPrompt(PlannerInput{
		Stage:        PlannerStageProbeResolved,
		UserQuestion: "龙井茶产地在哪里",
		ProbeResult: &KnowledgeProbeResult{
			Query:    "龙井茶产地在哪里",
			MaxScore: 0.82,
			Hits: []KnowledgeProbeHit{
				{DocumentName: "产品资料-龙井茶.md", SectionPath: "产地说明", Score: 0.82, ContentPreview: "龙井茶产地说明"},
			},
		},
	})

	if !strings.Contains(prompt, "当前阶段：probe_resolved") {
		t.Fatal("expected probe_resolved stage")
	}
	if strings.Contains(prompt, `"action": "knowledge_probe`) {
		t.Fatal("probe_resolved prompt should not ask for knowledge_probe")
	}
	if strings.Contains(prompt, `"action": "context_lookup`) {
		t.Fatal("probe_resolved prompt should not ask for context_lookup")
	}
	if !strings.Contains(prompt, "知识库轻量探测结果") {
		t.Fatal("expected probe result")
	}
	if !strings.Contains(prompt, "产品资料-龙井茶.md") {
		t.Fatal("expected probe hit")
	}
	if !strings.Contains(prompt, "通用解释、建议、示例或写作素材选择direct_answer") {
		t.Fatal("expected probe miss direct_answer instruction")
	}
	if !strings.Contains(prompt, "缺少具体业务对象或范围选择clarify") {
		t.Fatal("expected probe miss clarify instruction")
	}
	if !strings.Contains(prompt, "要求执行高风险操作选择reject") {
		t.Fatal("expected probe miss reject instruction")
	}
	if !strings.Contains(prompt, "需要实时公开信息选择web_search") {
		t.Fatal("expected probe miss web_search instruction")
	}
	if !strings.Contains(prompt, "不要因为probe无命中就默认knowledge_search") {
		t.Fatal("expected probe miss no default knowledge_search instruction")
	}
}

func TestBuildPlannerToolInfos(t *testing.T) {
	tools := buildPlannerToolInfos(PlannerInput{
		Tools: []ToolSpec{
			{Name: string(ActionContextLookup), Description: "读取会话历史"},
			{Name: string(ActionKnowledgeProbe), Description: "轻量探测知识库"},
			{Name: string(ActionKnowledgeSearch), Description: "查询知识库"},
			{Name: string(ActionClarify), Description: "追问用户"},
		},
	})

	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
	if !hasToolInfo(tools, ActionContextLookup) {
		t.Fatal("expected context_lookup tool")
	}
	if !hasToolInfo(tools, ActionKnowledgeProbe) {
		t.Fatal("expected knowledge_probe tool")
	}
	if !hasToolInfo(tools, ActionKnowledgeSearch) {
		t.Fatal("expected knowledge_search tool")
	}
	if !hasToolInfo(tools, ActionClarify) {
		t.Fatal("expected clarify tool")
	}
}

func TestKnowledgeProbeToolSchemaIsLightweight(t *testing.T) {
	tool := plannerToolInfo(ActionKnowledgeProbe, "轻量探测知识库")
	searchPlan := requiredObjectParam(t, tool, "search_plan")

	if !hasSubParam(searchPlan, "query") {
		t.Fatal("expected query in probe search_plan")
	}
	for _, name := range []string{"top_k", "query_rewrite_enabled", "hybrid_enabled", "rerank_enabled"} {
		if hasSubParam(searchPlan, name) {
			t.Fatalf("probe search_plan should not include %s", name)
		}
	}
}

func TestKnowledgeSearchToolSchemaKeepsSearchPlan(t *testing.T) {
	tool := plannerToolInfo(ActionKnowledgeSearch, "查询知识库")
	searchPlan := requiredObjectParam(t, tool, "search_plan")

	for _, name := range []string{
		"query",
		"top_k",
		"query_rewrite_enabled",
		"hybrid_enabled",
		"rerank_enabled",
		"reason",
	} {
		if !hasSubParam(searchPlan, name) {
			t.Fatalf("expected %s in knowledge_search search_plan", name)
		}
	}
}

func TestParseToolCallDecisionKnowledgeSearch(t *testing.T) {
	decision, err := ParseToolCallDecision([]schema.ToolCall{{
		Function: schema.FunctionCall{
			Name: string(ActionKnowledgeSearch),
			Arguments: `{
				"reason":"需要知识库依据",
				"search_plan":{
					"query":"报销流程办理步骤",
					"top_k":5,
					"query_rewrite_enabled":true,
					"hybrid_enabled":true,
					"rerank_enabled":true,
					"reason":"查询流程"
				}
			}`,
		},
	}}, PlannerInput{Tools: []ToolSpec{{Name: string(ActionKnowledgeSearch)}}})
	if err != nil {
		t.Fatalf("parse tool call: %v", err)
	}
	if decision.Action != ActionKnowledgeSearch {
		t.Fatalf("expected knowledge_search, got %s", decision.Action)
	}
	if decision.SearchPlan.Query != "报销流程办理步骤" {
		t.Fatalf("expected planned query, got %s", decision.SearchPlan.Query)
	}
	if decision.SearchPlan.TopK != 5 {
		t.Fatalf("expected top_k 5, got %d", decision.SearchPlan.TopK)
	}
}

func TestParseToolCallDecisionKnowledgeProbe(t *testing.T) {
	decision, err := ParseToolCallDecision([]schema.ToolCall{{
		Function: schema.FunctionCall{
			Name: string(ActionKnowledgeProbe),
			Arguments: `{
				"reason":"可能是企业产品",
				"search_plan":{
					"query":"龙井茶产地"
				}
			}`,
		},
	}}, PlannerInput{Tools: []ToolSpec{{Name: string(ActionKnowledgeProbe)}}})
	if err != nil {
		t.Fatalf("parse tool call: %v", err)
	}
	if decision.Action != ActionKnowledgeProbe {
		t.Fatalf("expected knowledge_probe, got %s", decision.Action)
	}
	if decision.SearchPlan.Query != "龙井茶产地" {
		t.Fatalf("expected probe query, got %s", decision.SearchPlan.Query)
	}
}

func TestParseToolCallDecisionSearchPlanString(t *testing.T) {
	decision, err := ParseToolCallDecision([]schema.ToolCall{{
		Function: schema.FunctionCall{
			Name:      string(ActionKnowledgeSearch),
			Arguments: `{"reason":"命中知识库","search_plan":"合同金额 个人网盘 安全规范"}`,
		},
	}}, PlannerInput{Tools: []ToolSpec{{Name: string(ActionKnowledgeSearch)}}})
	if err != nil {
		t.Fatalf("parse tool call: %v", err)
	}
	if decision.Action != ActionKnowledgeSearch {
		t.Fatalf("expected knowledge_search, got %s", decision.Action)
	}
	if decision.SearchPlan.Query != "合同金额 个人网盘 安全规范" {
		t.Fatalf("expected string search_plan as query, got %s", decision.SearchPlan.Query)
	}
}

func TestParseToolCallDecisionClarify(t *testing.T) {
	decision, err := ParseToolCallDecision([]schema.ToolCall{{
		Function: schema.FunctionCall{
			Name:      string(ActionClarify),
			Arguments: `{"reason":"缺少对象","clarify_question":"请补充流程名称"}`,
		},
	}}, PlannerInput{Tools: []ToolSpec{{Name: string(ActionClarify)}}})
	if err != nil {
		t.Fatalf("parse tool call: %v", err)
	}
	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
	if decision.ClarifyQuestion != "请补充流程名称" {
		t.Fatalf("expected clarify question, got %s", decision.ClarifyQuestion)
	}
}

func TestParseToolCallDecisionErrors(t *testing.T) {
	input := PlannerInput{Tools: []ToolSpec{{Name: string(ActionReject)}}}
	if _, err := ParseToolCallDecision(nil, input); err == nil {
		t.Fatal("expected error for empty tool calls")
	}
	if _, err := ParseToolCallDecision([]schema.ToolCall{
		{Function: schema.FunctionCall{Name: string(ActionReject), Arguments: `{"reason":"a"}`}},
		{Function: schema.FunctionCall{Name: string(ActionReject), Arguments: `{"reason":"b"}`}},
	}, input); err == nil {
		t.Fatal("expected error for multiple tool calls")
	}
	if _, err := ParseToolCallDecision([]schema.ToolCall{{
		Function: schema.FunctionCall{Name: "delete_database", Arguments: `{"reason":"x"}`},
	}}, input); err == nil {
		t.Fatal("expected error for unknown function")
	}
	if _, err := ParseToolCallDecision([]schema.ToolCall{{
		Function: schema.FunctionCall{Name: string(ActionReject), Arguments: `{bad json`},
	}}, input); err == nil {
		t.Fatal("expected error for invalid arguments")
	}
	if _, err := ParseToolCallDecision([]schema.ToolCall{{
		Function: schema.FunctionCall{Name: string(ActionReject), Arguments: `{}`},
	}}, input); err == nil {
		t.Fatal("expected error for missing reason")
	}
}

func hasToolInfo(tools []*schema.ToolInfo, action Action) bool {
	for _, tool := range tools {
		if tool.Name == string(action) {
			return true
		}
	}
	return false
}

func requiredObjectParam(t *testing.T, tool *schema.ToolInfo, name string) *schema.ParameterInfo {
	t.Helper()
	if tool == nil || tool.ParamsOneOf == nil {
		t.Fatal("expected tool params")
	}
	js, err := tool.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("to json schema: %v", err)
	}
	param, ok := js.Properties.Get(name)
	if !ok {
		t.Fatalf("expected %s param", name)
	}
	if param.Type != string(schema.Object) {
		t.Fatalf("expected %s to be object, got %s", name, param.Type)
	}
	info := &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{}}
	for pair := param.Properties.Oldest(); pair != nil; pair = pair.Next() {
		info.SubParams[pair.Key] = &schema.ParameterInfo{Type: schema.DataType(pair.Value.Type)}
	}
	return info
}

func hasSubParam(param *schema.ParameterInfo, name string) bool {
	_, ok := param.SubParams[name]
	return ok
}
