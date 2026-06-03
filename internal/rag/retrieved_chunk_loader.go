package rag

import (
	"context"

	"OurAgent/internal/model"
	"OurAgent/internal/repository"

	pkgerrors "github.com/pkg/errors"
)

type childHit struct {
	ChildID uint64
	Score   float64
}

// loadRetrievedChunks 根据子chunkID回查父chunk和文档信息
func loadRetrievedChunks(_ context.Context, docs *repository.DocumentRepository, chunks *repository.ChunkRepository, userID, kbID uint64, hits []childHit) ([]RetrievedChunk, error) {
	childIDs := make([]uint64, 0, len(hits))
	scoreByChildID := make(map[uint64]float64, len(hits))
	for _, hit := range hits {
		childIDs = append(childIDs, hit.ChildID)
		scoreByChildID[hit.ChildID] = hit.Score
	}

	children, err := chunks.FindChildrenByIDs(userID, kbID, childIDs)
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

	parents, err := chunks.FindParentsByIDs(userID, kbID, parentIDs)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询父文档切片失败")
	}
	parentByID := make(map[uint64]model.DocumentParentChunk, len(parents))
	for _, parent := range parents {
		parentByID[parent.ID] = parent
	}

	documents, err := docs.FindByIDsAndUserID(documentIDs, userID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档失败")
	}
	docByID := make(map[uint64]model.Document, len(documents))
	for _, doc := range documents {
		if doc.Status != model.DocumentStatusCompleted {
			continue
		}
		docByID[doc.ID] = doc
	}

	results := make([]RetrievedChunk, 0, len(hits))
	for _, hit := range hits {
		child, ok := childByID[hit.ChildID]
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
		doc, ok := docByID[child.DocumentID]
		if !ok {
			continue
		}
		results = append(results, RetrievedChunk{
			Chunk:        parent,
			MatchedChunk: child,
			Document:     doc,
			Score:        hit.Score,
		})
	}
	return results, nil
}
