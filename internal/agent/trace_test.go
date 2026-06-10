package agent

import "testing"

func TestTraceStepOrder(t *testing.T) {
	trace := NewTrace(IntentKnowledgeQA)
	trace.AddStep(Step{Tool: ToolKnowledgeSearch, Action: "invoke", Status: StatusLowConfidence})
	trace.AddStep(Step{Tool: ToolWebSearch, Action: "invoke", Status: StatusSuccess})

	if len(trace.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(trace.Steps))
	}
	if trace.Steps[0].Tool != ToolKnowledgeSearch || trace.Steps[1].Tool != ToolWebSearch {
		t.Fatalf("unexpected step order: %+v", trace.Steps)
	}
}
