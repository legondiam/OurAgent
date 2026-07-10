package agent

import "testing"

func TestNormalizeDecisionFallbacksInvalidAction(t *testing.T) {
	input := PlannerInput{UserQuestion: "报销流程怎么走", WebEnabled: true}
	defaults := SearchPlan{Query: "报销流程怎么走", TopK: 5, QueryRewriteEnabled: true, HybridEnabled: true, RerankEnabled: true}

	decision := NormalizeDecision(Decision{Action: "delete_database"}, input, defaults)

	if decision.Action != ActionKnowledgeSearch {
		t.Fatalf("expected knowledge_search, got %s", decision.Action)
	}
	if decision.SearchPlan.Query != input.UserQuestion {
		t.Fatalf("expected default query, got %s", decision.SearchPlan.Query)
	}
}

func TestNormalizeDecisionBlocksUnavailableWebSearch(t *testing.T) {
	input := PlannerInput{UserQuestion: "今天有什么新闻", WebEnabled: false}
	defaults := SearchPlan{Query: "今天有什么新闻", TopK: 5}

	decision := NormalizeDecision(Decision{Action: ActionWebSearch}, input, defaults)

	if decision.Action != ActionKnowledgeSearch {
		t.Fatalf("expected knowledge_search, got %s", decision.Action)
	}
}

func TestNormalizeDecisionLimitsTopK(t *testing.T) {
	input := PlannerInput{UserQuestion: "报销流程怎么走", WebEnabled: true}
	defaults := SearchPlan{Query: "报销流程怎么走", TopK: 5}
	decision := Decision{
		Action: ActionKnowledgeSearch,
		SearchPlan: SearchPlan{
			Query: "报销流程办理步骤",
			TopK:  99,
		},
	}

	normalized := NormalizeDecision(decision, input, defaults)

	if normalized.SearchPlan.TopK != 20 {
		t.Fatalf("expected top_k 20, got %d", normalized.SearchPlan.TopK)
	}
}

func TestNormalizeDecisionMergesDefaultSearchOptions(t *testing.T) {
	input := PlannerInput{UserQuestion: "合同金额能发个人网盘吗", WebEnabled: true}
	defaults := SearchPlan{
		Query:               input.UserQuestion,
		TopK:                8,
		QueryRewriteEnabled: true,
		HybridEnabled:       true,
		RerankEnabled:       true,
	}
	decision := Decision{
		Action:     ActionKnowledgeSearch,
		SearchPlan: SearchPlan{Query: "合同金额 个人网盘 安全规范"},
	}

	normalized := NormalizeDecision(decision, input, defaults)

	if normalized.SearchPlan.Query != "合同金额 个人网盘 安全规范" {
		t.Fatalf("expected planned query, got %s", normalized.SearchPlan.Query)
	}
	if normalized.SearchPlan.TopK != 8 {
		t.Fatalf("expected default top_k 8, got %d", normalized.SearchPlan.TopK)
	}
	if !normalized.SearchPlan.QueryRewriteEnabled {
		t.Fatal("expected default query rewrite enabled")
	}
	if !normalized.SearchPlan.HybridEnabled {
		t.Fatal("expected default hybrid enabled")
	}
	if !normalized.SearchPlan.RerankEnabled {
		t.Fatal("expected default rerank enabled")
	}
}

func TestNormalizePostRAGBlocksKnowledgeSearch(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePostRAG, UserQuestion: "报销流程怎么走", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionKnowledgeSearch}, input, SearchPlan{})

	if decision.Action != ActionWebSearch {
		t.Fatalf("expected web_search, got %s", decision.Action)
	}
}

func TestNormalizePostRAGRejectsUnavailableWebSearch(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePostRAG, UserQuestion: "报销流程怎么走", WebEnabled: false}

	decision := NormalizeDecision(Decision{Action: ActionWebSearch}, input, SearchPlan{})

	if decision.Action != ActionReject {
		t.Fatalf("expected reject, got %s", decision.Action)
	}
}

func TestNormalizePostRAGKeepsClarify(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePostRAG, UserQuestion: "这个流程怎么走", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionClarify}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
	if decision.ClarifyQuestion == "" {
		t.Fatal("expected default clarify question")
	}
}

func TestNormalizePreRAGAllowsContextLookup(t *testing.T) {
	input := PlannerInput{
		Stage:        PlannerStagePreRAG,
		UserQuestion: "为什么要部门审批",
		Tools:        []ToolSpec{{Name: string(ActionContextLookup)}},
		WebEnabled:   true,
	}

	decision := NormalizeDecision(Decision{Action: ActionContextLookup}, input, SearchPlan{})

	if decision.Action != ActionContextLookup {
		t.Fatalf("expected context_lookup, got %s", decision.Action)
	}
}

func TestNormalizePreRAGBlocksUnavailableContextLookup(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePreRAG, UserQuestion: "为什么要部门审批", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionContextLookup}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
}

func TestNormalizeContextResolvedBlocksContextLookup(t *testing.T) {
	input := PlannerInput{Stage: PlannerStageContextResolved, UserQuestion: "为什么要部门审批", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionContextLookup}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
}

func TestNormalizePostRAGBlocksContextLookup(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePostRAG, UserQuestion: "为什么要部门审批", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionContextLookup}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
}

func TestNormalizePreRAGAllowsDirectAnswer(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePreRAG, UserQuestion: "RAG是什么", WebEnabled: false}

	decision := NormalizeDecision(Decision{Action: ActionDirectAnswer}, input, SearchPlan{})

	if decision.Action != ActionDirectAnswer {
		t.Fatalf("expected direct_answer, got %s", decision.Action)
	}
}

func TestNormalizeContextResolvedAllowsDirectAnswer(t *testing.T) {
	input := PlannerInput{Stage: PlannerStageContextResolved, UserQuestion: "再简单点", WebEnabled: false}

	decision := NormalizeDecision(Decision{Action: ActionDirectAnswer}, input, SearchPlan{})

	if decision.Action != ActionDirectAnswer {
		t.Fatalf("expected direct_answer, got %s", decision.Action)
	}
}

func TestNormalizePostRAGBlocksDirectAnswer(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePostRAG, UserQuestion: "报销流程怎么走", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionDirectAnswer}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
}

func TestNormalizePreRAGAllowsKnowledgeProbe(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePreRAG, UserQuestion: "龙井茶产地在哪里", WebEnabled: false}
	defaults := SearchPlan{Query: "龙井茶产地在哪里", TopK: 5}

	decision := NormalizeDecision(Decision{Action: ActionKnowledgeProbe}, input, defaults)

	if decision.Action != ActionKnowledgeProbe {
		t.Fatalf("expected knowledge_probe, got %s", decision.Action)
	}
	if decision.SearchPlan.Query != input.UserQuestion {
		t.Fatalf("expected probe query, got %s", decision.SearchPlan.Query)
	}
}

func TestNormalizeContextResolvedAllowsKnowledgeProbe(t *testing.T) {
	input := PlannerInput{Stage: PlannerStageContextResolved, UserQuestion: "产地在哪里", WebEnabled: false}
	defaults := SearchPlan{Query: "龙井茶产地在哪里", TopK: 5}

	decision := NormalizeDecision(Decision{Action: ActionKnowledgeProbe}, input, defaults)

	if decision.Action != ActionKnowledgeProbe {
		t.Fatalf("expected knowledge_probe, got %s", decision.Action)
	}
}

func TestNormalizeProbeResolvedBlocksKnowledgeProbe(t *testing.T) {
	input := PlannerInput{Stage: PlannerStageProbeResolved, UserQuestion: "龙井茶产地在哪里", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionKnowledgeProbe}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
}

func TestNormalizeProbeResolvedBlocksContextLookup(t *testing.T) {
	input := PlannerInput{Stage: PlannerStageProbeResolved, UserQuestion: "龙井茶产地在哪里", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionContextLookup}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
}

func TestNormalizePostRAGBlocksKnowledgeProbe(t *testing.T) {
	input := PlannerInput{Stage: PlannerStagePostRAG, UserQuestion: "龙井茶产地在哪里", WebEnabled: true}

	decision := NormalizeDecision(Decision{Action: ActionKnowledgeProbe}, input, SearchPlan{})

	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify, got %s", decision.Action)
	}
}
