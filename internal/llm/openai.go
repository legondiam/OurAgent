package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

type OpenAICompatibleEmbedding struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewOpenAICompatibleEmbedding(baseURL, apiKey, model string) *OpenAICompatibleEmbedding {
	return &OpenAICompatibleEmbedding{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed 调用Embedding模型生成文本向量
func (p *OpenAICompatibleEmbedding) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if p.apiKey == "" {
		return nil, errors.New("模型 API Key 不能为空")
	}
	reqBody := map[string]interface{}{
		"model": p.model,
		"input": texts,
	}
	raw, err := p.do(ctx, "/embeddings", reqBody)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("Embedding 模型返回错误: %s", resp.Error.Message)
	}
	vectors := make([][]float64, 0, len(resp.Data))
	for _, item := range resp.Data {
		vectors = append(vectors, item.Embedding)
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("Embedding 返回数量不匹配: 实际 %d，期望 %d", len(vectors), len(texts))
	}
	return vectors, nil
}

func (p *OpenAICompatibleEmbedding) do(ctx context.Context, path string, body interface{}) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Embedding 请求失败: 状态码=%d，响应=%s", resp.StatusCode, buf.String())
	}
	return buf.Bytes(), nil
}
