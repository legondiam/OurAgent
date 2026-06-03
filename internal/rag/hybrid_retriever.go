package rag

import (
	"context"
	"sort"
)

const (
	recallSourceVector = "vector"
	recallSourceBM25   = "bm25"
)

type HybridRetriever struct {
	vector Retriever
	bm25   Retriever
}

func NewHybridRetriever(vector Retriever, bm25 Retriever) *HybridRetriever {
	return &HybridRetriever{vector: vector, bm25: bm25}
}

type fusionItem struct {
	chunk RetrievedChunk
	score float64
}

// Retrieve 执行向量召回和BM25召回并使用RRF融合
func (r *HybridRetriever) Retrieve(ctx context.Context, req RetrieveRequest) ([]RetrievedChunk, error) {
	// 初始化RRF参数和融合结果容器
	rrfK := req.RRFK
	if rrfK <= 0 {
		rrfK = 60
	}
	merged := make(map[uint64]*fusionItem)

	// 没有改写query时退化为原问题检索
	queries := req.Queries
	if len(queries) == 0 {
		queries = []RewrittenQuery{{Query: req.Query, Type: QueryTypeOriginal}}
	}

	// 向量召回使用原问题、改写问题和扩展问题
	for _, query := range queries {
		// 每个query独立走一次Qdrant向量召回
		results, err := r.vector.Retrieve(ctx, RetrieveRequest{
			UserID:          req.UserID,
			KnowledgeBaseID: req.KnowledgeBaseID,
			Query:           query.Query,
			TopK:            req.TopK,
		})
		if err != nil {
			return nil, err
		}
		for rank, item := range results {
			// 记录命中的query和召回通道，并按排名贡献RRF分数
			item.MatchedQueries = appendQuery(item.MatchedQueries, query.Query)
			item.RecallSources = appendQuery(item.RecallSources, recallSourceVector)
			item.VectorScore = item.Score
			addFusionResult(merged, item, rrfContribution(rrfK, rank+1), recallSourceVector)
		}
	}

	// BM25只使用用户原始问题，避免改写query稀释关键词
	if req.HybridEnabled && req.BM25Enabled && r.bm25 != nil {
		// 关键词召回只跑原问题，用于保留字段名、错误码和专有名词
		results, err := r.bm25.Retrieve(ctx, RetrieveRequest{
			UserID:          req.UserID,
			KnowledgeBaseID: req.KnowledgeBaseID,
			Query:           req.Query,
			BM25TopK:        req.BM25TopK,
		})
		if err != nil {
			if req.Trace != nil {
				req.Trace.BM25Error = err.Error()
			}
		} else {
			for rank, item := range results {
				// BM25结果同样按排名贡献RRF分数
				item.RecallSources = appendQuery(item.RecallSources, recallSourceBM25)
				item.BM25Score = item.Score
				addFusionResult(merged, item, rrfContribution(rrfK, rank+1), recallSourceBM25)
			}
		}
	}

	// 将融合map转为列表，RRF用于排序，原始召回分数用于阈值判断
	results := make([]RetrievedChunk, 0, len(merged))
	for _, item := range merged {
		item.chunk.RRFScore = item.score
		if item.chunk.VectorScore > 0 {
			item.chunk.Score = item.chunk.VectorScore
		} else if item.chunk.BM25Score > 0 {
			item.chunk.Score = item.chunk.BM25Score
		} else {
			item.chunk.Score = item.score
		}
		results = append(results, item.chunk)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].RRFScore > results[j].RRFScore
	})
	return results, nil
}

func addFusionResult(merged map[uint64]*fusionItem, item RetrievedChunk, score float64, source string) {
	// 以子chunkID作为多路召回的去重键
	childID := item.MatchedChunk.ID
	existing, ok := merged[childID]
	if !ok {
		// 首次命中时保存召回结果和本路RRF贡献
		item.RecallSources = appendQuery(item.RecallSources, source)
		merged[childID] = &fusionItem{chunk: item, score: score}
		return
	}

	// 重复命中时累加RRF分数，并合并召回来源和命中query
	existing.score += score
	existing.chunk.RecallSources = appendQuery(existing.chunk.RecallSources, source)
	existing.chunk.MatchedQueries = appendQueries(existing.chunk.MatchedQueries, item.MatchedQueries)

	// 保留各召回通道的最高原始分数用于trace和阈值判断
	if item.VectorScore > existing.chunk.VectorScore {
		existing.chunk.VectorScore = item.VectorScore
	}
	if item.BM25Score > existing.chunk.BM25Score {
		existing.chunk.BM25Score = item.BM25Score
	}
}

func rrfContribution(k, rank int) float64 {
	return 1 / float64(k+rank)
}
