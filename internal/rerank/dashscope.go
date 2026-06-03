package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"OurAgent/internal/rag"

	pkgerrors "github.com/pkg/errors"
)

type DashScopeReranker struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewDashScopeReranker(baseURL, apiKey, model string, timeout time.Duration) *DashScopeReranker {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &DashScopeReranker{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		client:  &http.Client{Timeout: timeout},
	}
}

// Rerank 调用DashScope重排序接口精排候选切片
func (r *DashScopeReranker) Rerank(ctx context.Context, req rag.RerankRequest) (*rag.RerankResult, error) {
	if r.apiKey == "" {
		return nil, pkgerrors.New("重排序APIKey不能为空")
	}
	if r.baseURL == "" {
		return nil, pkgerrors.New("重排序接口地址不能为空")
	}
	if r.model == "" {
		return nil, pkgerrors.New("重排序模型不能为空")
	}
	if len(req.Items) == 0 {
		return &rag.RerankResult{Items: []rag.RerankResultItem{}}, nil
	}

	body := dashScopeRequest{
		Model: r.model,
		Input: dashScopeInput{
			Query:     req.Query,
			Documents: buildDocuments(req.Items),
		},
		Parameters: dashScopeParameters{
			ReturnDocuments: false,
			TopN:            req.TopN,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "序列化重排序请求失败")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL, bytes.NewReader(raw))
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "创建重排序请求失败")
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "调用重排序接口失败")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "读取重排序响应失败")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("调用重排序接口失败: 状态码=%d, 响应=%s", resp.StatusCode, string(respBody))
	}

	var result dashScopeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, pkgerrors.WithMessage(err, "解析重排序响应失败")
	}
	items := make([]rag.RerankResultItem, 0, len(result.Output.Results))
	for _, item := range result.Output.Results {
		items = append(items, rag.RerankResultItem{
			Index: item.Index,
			Score: item.RelevanceScore,
		})
	}
	return &rag.RerankResult{Items: items}, nil
}

type dashScopeRequest struct {
	Model      string              `json:"model"`
	Input      dashScopeInput      `json:"input"`
	Parameters dashScopeParameters `json:"parameters"`
}

type dashScopeInput struct {
	Query     string              `json:"query"`
	Documents []dashScopeDocument `json:"documents"`
}

type dashScopeDocument struct {
	Text string `json:"text"`
}

type dashScopeParameters struct {
	ReturnDocuments bool `json:"return_documents"`
	TopN            int  `json:"top_n,omitempty"`
}

type dashScopeResponse struct {
	Output struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	} `json:"output"`
}

func buildDocuments(items []rag.RerankItem) []dashScopeDocument {
	documents := make([]dashScopeDocument, 0, len(items))
	for _, item := range items {
		documents = append(documents, dashScopeDocument{Text: item.Text})
	}
	return documents
}
