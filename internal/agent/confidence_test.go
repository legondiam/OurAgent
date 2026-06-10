package agent

import (
	"testing"

	"OurAgent/internal/rag"
)

func TestIsLowConfidence(t *testing.T) {
	prepared := &rag.PreparedChat{
		Answer: rag.FallbackAnswer,
		Trace: rag.RetrievalTrace{
			UsedChunkCount: 0,
			RejectReason:   "没有检索到可用切片",
		},
	}
	if !IsLowConfidence(prepared) {
		t.Fatal("expected low confidence")
	}
}

func TestIsLowConfidenceWithUsedChunk(t *testing.T) {
	prepared := &rag.PreparedChat{
		Answer: rag.FallbackAnswer,
		Trace: rag.RetrievalTrace{
			UsedChunkCount: 1,
			RejectReason:   "没有检索到可用切片",
		},
	}
	if IsLowConfidence(prepared) {
		t.Fatal("expected normal confidence when chunks are used")
	}
}
