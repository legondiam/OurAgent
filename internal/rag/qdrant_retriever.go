package rag

import (
	"context"
	"sort"

	"OurAgent/internal/repository"
	"OurAgent/internal/vectorstore"

	"github.com/cloudwego/eino/components/embedding"
	pkgerrors "github.com/pkg/errors"
)

type QdrantRetriever struct {
	docs     *repository.DocumentRepository
	chunks   *repository.ChunkRepository
	qdrant   *vectorstore.QdrantClient
	embedder embedding.Embedder
}

func NewQdrantRetriever(docs *repository.DocumentRepository, chunks *repository.ChunkRepository, qdrant *vectorstore.QdrantClient, embedder embedding.Embedder) *QdrantRetriever {
	return &QdrantRetriever{docs: docs, chunks: chunks, qdrant: qdrant, embedder: embedder}
}

// Retrieve 检索知识库相关文档切片
func (r *QdrantRetriever) Retrieve(ctx context.Context, req RetrieveRequest) ([]RetrievedChunk, error) {
	// 将用户问题转成向量
	vectors, err := r.embedder.EmbedStrings(ctx, []string{req.Query})
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "问题向量化失败")
	}

	// 在Qdrant中按用户和知识库范围检索最相关的切片ID
	hits, err := r.qdrant.Search(ctx, vectors[0], req.UserID, req.KnowledgeBaseID, req.TopK)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "检索失败")
	}
	if len(hits) == 0 {
		return []RetrievedChunk{}, nil
	}

	childHits := make([]childHit, 0, len(hits))
	for _, hit := range hits {
		childHits = append(childHits, childHit{ChildID: hit.ChunkID, Score: hit.Score})
	}
	results, err := loadRetrievedChunks(ctx, r.docs, r.chunks, req.UserID, req.KnowledgeBaseID, childHits)
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].VectorScore = results[i].Score
		results[i].RecallSources = appendQuery(results[i].RecallSources, "vector")
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}
