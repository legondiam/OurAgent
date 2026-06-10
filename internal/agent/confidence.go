package agent

import "OurAgent/internal/rag"

// IsLowConfidence判断RAG结果是否证据不足
func IsLowConfidence(prepared *rag.PreparedChat) bool {
	if prepared == nil {
		return true
	}
	return prepared.Answer == rag.FallbackAnswer &&
		prepared.Trace.UsedChunkCount == 0 &&
		prepared.Trace.RejectReason != ""
}
