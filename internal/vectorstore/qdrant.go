package vectorstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type QdrantClient struct {
	baseURL    string
	collection string
	client     *http.Client
}

type SearchHit struct {
	ChunkID    uint64
	DocumentID uint64
	Score      float64
	Payload    map[string]interface{}
}

func NewQdrantClient(baseURL, collection string) *QdrantClient {
	return &QdrantClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		collection: collection,
		client:     &http.Client{Timeout: 60 * time.Second},
	}
}

// EnsureCollection 确保 Qdrant 集合存在
func (c *QdrantClient) EnsureCollection(ctx context.Context, vectorSize int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/collections/"+c.collection, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("检查 Qdrant 集合失败: 状态码=%d", resp.StatusCode)
	}
	//collection不存在，创建
	body := map[string]interface{}{
		"vectors": map[string]interface{}{
			"size":     vectorSize,
			"distance": "Cosine",
		},
	}
	return c.do(ctx, http.MethodPut, "/collections/"+c.collection, body, nil)
}

// Upsert 写入或更新向量点
func (c *QdrantClient) Upsert(ctx context.Context, pointID uint64, vector []float64, payload map[string]interface{}) error {
	body := map[string]interface{}{
		"points": []map[string]interface{}{
			{
				"id":      pointID,
				"vector":  vector,
				"payload": payload,
			},
		},
	}
	return c.do(ctx, http.MethodPut, "/collections/"+c.collection+"/points?wait=true", body, nil)
}

// Search 按用户和知识库过滤检索相似向量
func (c *QdrantClient) Search(ctx context.Context, vector []float64, userID, knowledgeBaseID uint64, limit int) ([]SearchHit, error) {
	body := map[string]interface{}{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
		"filter": map[string]interface{}{
			"must": []map[string]interface{}{
				{"key": "user_id", "match": map[string]interface{}{"value": userID}},
				{"key": "knowledge_base_id", "match": map[string]interface{}{"value": knowledgeBaseID}},
			},
		},
	}

	var resp struct {
		Result []struct {
			ID      interface{}            `json:"id"`
			Score   float64                `json:"score"`
			Payload map[string]interface{} `json:"payload"`
		} `json:"result"`
		Status string `json:"status"`
	}
	//发起搜索请求
	if err := c.do(ctx, http.MethodPost, "/collections/"+c.collection+"/points/search", body, &resp); err != nil {
		return nil, err
	}
	//解析到SearchHit
	hits := make([]SearchHit, 0, len(resp.Result))
	for _, item := range resp.Result {
		chunkID := payloadUint(item.Payload, "chunk_id")
		if chunkID == 0 {
			chunkID = idToUint(item.ID)
		}
		hits = append(hits, SearchHit{
			ChunkID:    chunkID,
			DocumentID: payloadUint(item.Payload, "document_id"),
			Score:      item.Score,
			Payload:    item.Payload,
		})
	}
	return hits, nil
}

func (c *QdrantClient) do(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Qdrant 请求失败: 状态码=%d，响应=%s", resp.StatusCode, buf.String())
	}
	if out != nil {
		return json.Unmarshal(buf.Bytes(), out)
	}
	return nil
}

func payloadUint(payload map[string]interface{}, key string) uint64 {
	value, ok := payload[key]
	if !ok {
		return 0
	}
	return idToUint(value)
}

func idToUint(value interface{}) uint64 {
	switch v := value.(type) {
	case float64:
		return uint64(v)
	case int:
		return uint64(v)
	case uint64:
		return v
	case string:
		n, _ := strconv.ParseUint(v, 10, 64)
		return n
	default:
		return 0
	}
}
