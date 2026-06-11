package agent

import (
	"testing"

	"OurAgent/internal/rag"
)

func TestPlannerClarifyAfterRAGForVagueQuestion(t *testing.T) {
	planner := NewPostRAGPlanner()
	decision := planner.ClarifyAfterRAG("这个流程怎么走", rag.RetrievalTrace{
		UsedChunkCount: 0,
		RewrittenQueries: []rag.TraceQuery{
			{Query: "这个流程的办理步骤", Type: rag.QueryTypeRewrite},
		},
	})
	if !decision.NeedClarify {
		t.Fatal("expected clarify decision")
	}
}

func TestPlannerDoesNotClarifyWhenQuestionHasObject(t *testing.T) {
	planner := NewPostRAGPlanner()
	decision := planner.ClarifyAfterRAG("报销这个流程怎么走", rag.RetrievalTrace{
		UsedChunkCount: 0,
		RewrittenQueries: []rag.TraceQuery{
			{Query: "报销流程如何办理", Type: rag.QueryTypeRewrite},
		},
	})
	if decision.NeedClarify {
		t.Fatal("expected no clarify when business object exists")
	}
}

func TestPlannerDoesNotClarifyWhenChunkUsed(t *testing.T) {
	planner := NewPostRAGPlanner()
	decision := planner.ClarifyAfterRAG("这个流程怎么走", rag.RetrievalTrace{
		UsedChunkCount: 1,
	})
	if decision.NeedClarify {
		t.Fatal("expected no clarify when RAG found usable context")
	}
}
