package agent

import (
	"strings"
	"testing"
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
	if !strings.Contains(prompt, `"action": "clarify | web_search | reject"`) {
		t.Fatal("expected post_rag action schema")
	}
	if strings.Contains(prompt, `"search_plan"`) {
		t.Fatal("post_rag prompt should not ask for search_plan")
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

	if !strings.Contains(prompt, `"action": "context_lookup | clarify | knowledge_search | web_search | reject"`) {
		t.Fatal("expected context_lookup action schema")
	}
	if !strings.Contains(prompt, "优先选择context_lookup") {
		t.Fatal("expected context lookup instruction")
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
	if !strings.Contains(prompt, "独立完整检索问题") {
		t.Fatal("expected standalone query instruction")
	}
}
