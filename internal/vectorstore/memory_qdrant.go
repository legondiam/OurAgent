package vectorstore

import (
	"context"
	"strings"
)

type MemorySearchFilter struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Statuses        []string
}

type MemoryVectorHit struct {
	MemoryID uint64
	Score    float64
}

type MemoryQdrantStore struct {
	client *QdrantClient
}

// NewMemoryQdrantStore 创建长期记忆向量存储
func NewMemoryQdrantStore(baseURL, collection string) *MemoryQdrantStore {
	return &MemoryQdrantStore{client: NewQdrantClient(baseURL, collection)}
}

// EnsureCollection 确保长期记忆Collection存在
func (s *MemoryQdrantStore) EnsureCollection(ctx context.Context, vectorSize int) error {
	return s.client.EnsureCollection(ctx, vectorSize)
}

// Upsert 写入指定记忆版本的向量和过滤Payload
func (s *MemoryQdrantStore) Upsert(ctx context.Context, memoryID, version uint64, vector []float64, payload map[string]any) error {
	payload["memory_id"] = memoryID
	payload["version"] = version
	return s.client.Upsert(ctx, memoryID, vector, payload)
}

// Search 按用户、知识库和状态过滤语义召回结果
func (s *MemoryQdrantStore) Search(ctx context.Context, vector []float64, filter MemorySearchFilter, limit int) ([]MemoryVectorHit, error) {
	must := []map[string]any{
		{"key": "user_id", "match": map[string]any{"value": filter.UserID}},
		{"key": "knowledge_base_id", "match": map[string]any{"value": filter.KnowledgeBaseID}},
	}
	if len(filter.Statuses) > 0 {
		must = append(must, map[string]any{"key": "status", "match": map[string]any{"any": filter.Statuses}})
	}
	body := map[string]any{"vector": vector, "limit": limit, "with_payload": true, "filter": map[string]any{"must": must}}
	var response struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := s.client.do(ctx, "POST", "/collections/"+s.client.collection+"/points/search", body, &response); err != nil {
		return nil, err
	}
	hits := make([]MemoryVectorHit, 0, len(response.Result))
	for _, item := range response.Result {
		id := payloadUint(item.Payload, "memory_id")
		if id == 0 {
			id = idToUint(item.ID)
		}
		hits = append(hits, MemoryVectorHit{MemoryID: id, Score: item.Score})
	}
	return hits, nil
}

// Delete 幂等删除长期记忆向量
func (s *MemoryQdrantStore) Delete(ctx context.Context, memoryID uint64) error {
	body := map[string]any{"points": []uint64{memoryID}}
	err := s.client.do(ctx, "POST", "/collections/"+s.client.collection+"/points/delete?wait=true", body, nil)
	if err != nil && strings.Contains(err.Error(), "状态码=404") {
		return nil
	}
	return err
}
