package service

import (
	"context"
	"sort"
	"time"

	"OurAgent/internal/agent"
	"OurAgent/internal/config"
	"OurAgent/internal/document"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	"OurAgent/internal/vectorstore"

	"github.com/cloudwego/eino/components/embedding"
)

type LongTermMemoryRetrieveRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
	CurrentTopic    string
}

type LongTermMemoryRetriever struct {
	repo     *repository.LongTermMemoryRepository
	vectors  *vectorstore.MemoryQdrantStore
	embedder embedding.Embedder
	cfg      config.LongTermMemoryConfig
	gate     MemoryRecallGate
}

// NewLongTermMemoryRetriever 创建长期记忆召回器
func NewLongTermMemoryRetriever(repo *repository.LongTermMemoryRepository, vectors *vectorstore.MemoryQdrantStore, embedder embedding.Embedder, cfg config.LongTermMemoryConfig) *LongTermMemoryRetriever {
	return &LongTermMemoryRetriever{repo: repo, vectors: vectors, embedder: embedder, cfg: cfg}
}

// Retrieve 合并固定、词面和按需语义召回结果
func (r *LongTermMemoryRetriever) Retrieve(ctx context.Context, req LongTermMemoryRetrieveRequest) (agent.LongTermMemoryContext, error) {
	result := agent.LongTermMemoryContext{}
	if !r.cfg.Enabled {
		return result, nil
	}
	fixed, err := r.repo.FindFixedAndLexical(req.UserID, req.KnowledgeBaseID, req.Question, r.finalTopK()*2)
	if err != nil {
		return result, err
	}
	seen := make(map[uint64]struct{}, len(fixed))
	for _, memory := range fixed {
		seen[memory.ID] = struct{}{}
		result.Items = append(result.Items, toMemoryItem(memory, 1))
		if memory.Scope == model.MemoryScopeUserGlobal {
			result.FixedCount++
		} else {
			result.LexicalCount++
		}
	}
	decision := r.gate.Evaluate(req.Question, false)
	result.SemanticRecallTriggered = decision.Semantic && r.cfg.SemanticRecallEnabled
	result.RecallGateReason = decision.Reason
	if result.SemanticRecallTriggered && r.embedder != nil && r.vectors != nil {
		timeout := r.cfg.RetrievalTimeoutSeconds
		if timeout <= 0 {
			timeout = 3
		}
		semanticCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		vectors, embedErr := r.embedder.EmbedStrings(semanticCtx, []string{req.Question + "\n" + req.CurrentTopic})
		if embedErr != nil || len(vectors) == 0 {
			result.SemanticRetrievalDegraded = true
		} else {
			hits, searchErr := r.vectors.Search(semanticCtx, vectors[0], vectorstore.MemorySearchFilter{UserID: req.UserID, KnowledgeBaseID: req.KnowledgeBaseID, Statuses: []string{model.MemoryStatusActive}}, r.semanticTopK())
			if searchErr != nil {
				result.SemanticRetrievalDegraded = true
			} else {
				ids, scores := make([]uint64, 0, len(hits)), make(map[uint64]float64)
				for _, hit := range hits {
					if hit.Score >= r.similarityThreshold() {
						ids = append(ids, hit.MemoryID)
						scores[hit.MemoryID] = hit.Score
					}
				}
				memories, lookupErr := r.repo.FindActiveByIDs(req.UserID, req.KnowledgeBaseID, ids)
				if lookupErr != nil {
					result.SemanticRetrievalDegraded = true
				} else {
					for _, memory := range memories {
						if _, ok := seen[memory.ID]; ok {
							continue
						}
						seen[memory.ID] = struct{}{}
						result.Items = append(result.Items, toMemoryItem(memory, scores[memory.ID]))
						result.SemanticCount++
					}
				}
			}
		}
	}
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].Score > result.Items[j].Score })
	result.Items = trimMemoryItems(result.Items, r.finalTopK(), r.maxContextTokens())
	for _, item := range result.Items {
		result.EstimatedTokens += document.EstimateTokens(item.Content)
	}
	return result, nil
}

func toMemoryItem(memory model.LongTermMemory, score float64) agent.LongTermMemoryItem {
	return agent.LongTermMemoryItem{MemoryID: memory.ID, Type: memory.Type, Scope: memory.Scope, Content: memory.Content, Score: score + memory.Importance*0.05}
}

func trimMemoryItems(items []agent.LongTermMemoryItem, limit, maxTokens int) []agent.LongTermMemoryItem {
	if limit <= 0 {
		limit = 6
	}
	if maxTokens <= 0 {
		maxTokens = 1200
	}
	selected, tokens := make([]agent.LongTermMemoryItem, 0, limit), 0
	for _, item := range items {
		itemTokens := document.EstimateTokens(item.Content)
		if len(selected) >= limit || tokens+itemTokens > maxTokens {
			continue
		}
		selected = append(selected, item)
		tokens += itemTokens
	}
	return selected
}

func (r *LongTermMemoryRetriever) finalTopK() int {
	if r.cfg.FinalTopK > 0 {
		return r.cfg.FinalTopK
	}
	return 6
}
func (r *LongTermMemoryRetriever) semanticTopK() int {
	if r.cfg.SemanticTopK > 0 {
		return r.cfg.SemanticTopK
	}
	return 12
}
func (r *LongTermMemoryRetriever) similarityThreshold() float64 {
	if r.cfg.SimilarityThreshold > 0 {
		return r.cfg.SimilarityThreshold
	}
	return 0.70
}
func (r *LongTermMemoryRetriever) maxContextTokens() int {
	if r.cfg.MaxContextTokens > 0 {
		return r.cfg.MaxContextTokens
	}
	return 1200
}
