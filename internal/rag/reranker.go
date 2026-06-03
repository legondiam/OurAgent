package rag

import "context"

type RerankRequest struct {
	Query string
	Items []RerankItem
	TopN  int
}

type RerankItem struct {
	Index         int
	ChildChunkID  uint64
	ParentChunkID uint64
	Text          string
}

type RerankResult struct {
	Items []RerankResultItem
}

type RerankResultItem struct {
	Index int
	Score float64
}

type FallbackReranker struct{}

func NewFallbackReranker() *FallbackReranker {
	return &FallbackReranker{}
}

// Rerank 按原召回顺序返回候选结果
func (r *FallbackReranker) Rerank(_ context.Context, req RerankRequest) (*RerankResult, error) {
	items := make([]RerankResultItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, RerankResultItem{Index: item.Index})
	}
	return &RerankResult{Items: items}, nil
}
