package rag

import (
	"context"
	"sort"

	"OurAgent/internal/repository"
	appsearch "OurAgent/internal/search"

	pkgerrors "github.com/pkg/errors"
)

type BM25Retriever struct {
	docs   *repository.DocumentRepository
	chunks *repository.ChunkRepository
	store  appsearch.KeywordStore
}

func NewBM25Retriever(docs *repository.DocumentRepository, chunks *repository.ChunkRepository, store appsearch.KeywordStore) *BM25Retriever {
	return &BM25Retriever{docs: docs, chunks: chunks, store: store}
}

// Retrieve 使用BM25关键词检索召回子chunk
func (r *BM25Retriever) Retrieve(ctx context.Context, req RetrieveRequest) ([]RetrievedChunk, error) {
	hits, err := r.store.Search(ctx, appsearch.KeywordSearchRequest{
		UserID:          req.UserID,
		KnowledgeBaseID: req.KnowledgeBaseID,
		Query:           req.Query,
		Limit:           req.BM25TopK,
	})
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "BM25检索失败")
	}
	if len(hits) == 0 {
		return []RetrievedChunk{}, nil
	}
	childHits := make([]childHit, 0, len(hits))
	for _, hit := range hits {
		childHits = append(childHits, childHit{ChildID: hit.ChildChunkID, Score: hit.Score})
	}
	results, err := loadRetrievedChunks(ctx, r.docs, r.chunks, req.UserID, req.KnowledgeBaseID, childHits)
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].BM25Score = results[i].Score
		results[i].RecallSources = appendQuery(results[i].RecallSources, "bm25")
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}
