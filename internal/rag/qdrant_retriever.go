package rag

import (
	"context"
	"sort"

	"OurAgent/internal/llm"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	"OurAgent/internal/vectorstore"

	pkgerrors "github.com/pkg/errors"
)

type QdrantRetriever struct {
	docs     *repository.DocumentRepository
	chunks   *repository.ChunkRepository
	qdrant   *vectorstore.QdrantClient
	embedder llm.EmbeddingProvider
}

func NewQdrantRetriever(docs *repository.DocumentRepository, chunks *repository.ChunkRepository, qdrant *vectorstore.QdrantClient, embedder llm.EmbeddingProvider) *QdrantRetriever {
	return &QdrantRetriever{docs: docs, chunks: chunks, qdrant: qdrant, embedder: embedder}
}

// Retrieve 检索知识库相关文档切片
func (r *QdrantRetriever) Retrieve(ctx context.Context, req RetrieveRequest) ([]RetrievedChunk, error) {
	// 将用户问题转成向量
	vectors, err := r.embedder.Embed(ctx, []string{req.Query})
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

	chunkIDs := make([]uint64, 0, len(hits))
	scoreByChunkID := make(map[uint64]float64, len(hits))
	for _, hit := range hits {
		chunkIDs = append(chunkIDs, hit.ChunkID)
		scoreByChunkID[hit.ChunkID] = hit.Score
	}

	// 回查MySQL获取切片正文，Qdrant只负责召回和相似度分数
	chunks, err := r.chunks.FindByIDs(req.UserID, req.KnowledgeBaseID, chunkIDs)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档切片失败")
	}
	chunkByID := make(map[uint64]model.DocumentChunk, len(chunks))
	documentIDs := make([]uint64, 0, len(chunks))
	for _, chunk := range chunks {
		chunkByID[chunk.ID] = chunk
		documentIDs = append(documentIDs, chunk.DocumentID)
	}

	// 回查文档信息，用于生成来源名称和检索trace
	docs, err := r.docs.FindByIDsAndUserID(documentIDs, req.UserID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档失败")
	}
	docByID := make(map[uint64]model.Document, len(docs))
	for _, doc := range docs {
		docByID[doc.ID] = doc
	}

	// 按Qdrant命中结果组装完整切片，并保留对应相似度分数
	results := make([]RetrievedChunk, 0, len(hits))
	for _, hit := range hits {
		chunk, ok := chunkByID[hit.ChunkID]
		if !ok {
			continue
		}
		results = append(results, RetrievedChunk{
			Chunk:    chunk,
			Document: docByID[chunk.DocumentID],
			Score:    scoreByChunkID[chunk.ID],
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}
