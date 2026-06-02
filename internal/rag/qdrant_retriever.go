package rag

import (
	"context"
	"sort"

	"OurAgent/internal/model"
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

	childIDs := make([]uint64, 0, len(hits))
	scoreByChildID := make(map[uint64]float64, len(hits))
	for _, hit := range hits {
		childIDs = append(childIDs, hit.ChunkID)
		scoreByChildID[hit.ChunkID] = hit.Score
	}

	// 回查MySQL获取命中的子chunk
	children, err := r.chunks.FindChildrenByIDs(req.UserID, req.KnowledgeBaseID, childIDs)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档切片失败")
	}
	childByID := make(map[uint64]model.DocumentChildChunk, len(children))
	parentIDs := make([]uint64, 0, len(children))
	documentIDs := make([]uint64, 0, len(children))
	for _, child := range children {
		childByID[child.ID] = child
		if child.ParentChunkID > 0 {
			parentIDs = append(parentIDs, child.ParentChunkID)
		} else {
			parentIDs = append(parentIDs, child.ID)
		}
		documentIDs = append(documentIDs, child.DocumentID)
	}

	// 回查父chunk，LLM阅读父chunk内容
	parents, err := r.chunks.FindParentsByIDs(req.UserID, req.KnowledgeBaseID, parentIDs)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询父文档切片失败")
	}
	parentByID := make(map[uint64]model.DocumentParentChunk, len(parents))
	for _, parent := range parents {
		parentByID[parent.ID] = parent
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
		child, ok := childByID[hit.ChunkID]
		if !ok {
			continue
		}
		parentID := child.ParentChunkID
		if parentID == 0 {
			parentID = child.ID
		}
		parent, ok := parentByID[parentID]
		if !ok {
			continue
		}
		results = append(results, RetrievedChunk{
			Chunk:        parent,
			MatchedChunk: child,
			Document:     docByID[child.DocumentID],
			Score:        scoreByChildID[child.ID],
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}
