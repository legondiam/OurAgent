package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"OurAgent/internal/config"
)

const defaultDashScopeEndpoint = "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation"

type DashScopeAnswerer struct {
	client     *http.Client
	endpoint   string
	apiKey     string
	model      string
	timeout    time.Duration
	enableSrc  bool
	disclaimer string
}

func NewDashScopeAnswerer(cfg config.WebSearchConfig) *DashScopeAnswerer {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultDashScopeEndpoint
	}
	return &DashScopeAnswerer{
		client:     &http.Client{Timeout: timeout},
		endpoint:   endpoint,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		timeout:    timeout,
		enableSrc:  cfg.EnableSource,
		disclaimer: cfg.Disclaimer,
	}
}

func (a *DashScopeAnswerer) Answer(ctx context.Context, req Request) (*Result, error) {
	if strings.TrimSpace(a.apiKey) == "" {
		return nil, errors.New("DashScope APIKey不能为空")
	}
	if strings.TrimSpace(a.model) == "" {
		return nil, errors.New("DashScope联网搜索模型不能为空")
	}

	body := dashScopeRequest{
		Model: a.model,
		Input: dashScopeInput{
			Messages: []dashScopeMessage{
				{Role: "system", Content: webFallbackSystemPrompt},
				{Role: "user", Content: buildUserPrompt(req.Question, a.disclaimer)},
			},
		},
		Parameters: dashScopeParameters{
			ResultFormat: "message",
			EnableSearch: true,
			SearchOptions: dashScopeSearchOptions{
				ForcedSearch:   true,
				EnableSource:   a.enableSrc,
				EnableCitation: true,
				SearchStrategy: "turbo",
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("DashScope联网搜索请求失败，timeout=%s: %w", a.timeout, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DashScope联网搜索失败: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var parsed dashScopeResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	if strings.TrimSpace(parsed.Code) != "" {
		return nil, fmt.Errorf("DashScope联网搜索失败: %s %s", parsed.Code, parsed.Message)
	}

	answer := strings.TrimSpace(parsed.Output.firstContent())
	if answer == "" {
		return nil, errors.New("DashScope联网搜索返回空回答")
	}
	answer = ensureDisclaimer(answer, a.disclaimer)
	return &Result{Answer: answer, Sources: parsed.Output.searchSources()}, nil
}

type dashScopeRequest struct {
	Model      string              `json:"model"`
	Input      dashScopeInput      `json:"input"`
	Parameters dashScopeParameters `json:"parameters"`
}

type dashScopeInput struct {
	Messages []dashScopeMessage `json:"messages"`
}

type dashScopeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type dashScopeParameters struct {
	ResultFormat  string                 `json:"result_format"`
	EnableSearch  bool                   `json:"enable_search"`
	SearchOptions dashScopeSearchOptions `json:"search_options"`
}

type dashScopeSearchOptions struct {
	ForcedSearch   bool   `json:"forced_search"`
	EnableSource   bool   `json:"enable_source"`
	EnableCitation bool   `json:"enable_citation"`
	SearchStrategy string `json:"search_strategy"`
}

type dashScopeResponse struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Output  dashScopeOutput `json:"output"`
}

type dashScopeOutput struct {
	Choices    []dashScopeChoice `json:"choices"`
	Text       string            `json:"text"`
	SearchInfo dashScopeSearch   `json:"search_info"`
}

type dashScopeChoice struct {
	Message dashScopeMessage `json:"message"`
}

type dashScopeSearch struct {
	SearchResults []dashScopeSearchResult `json:"search_results"`
}

type dashScopeSearchResult struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Summary string `json:"summary"`
	Content string `json:"content"`
}

func (o dashScopeOutput) firstContent() string {
	if len(o.Choices) == 0 {
		return o.Text
	}
	return contentText(o.Choices[0].Message.Content)
}

func (o dashScopeOutput) searchSources() []Source {
	sources := make([]Source, 0, len(o.SearchInfo.SearchResults))
	for _, item := range o.SearchInfo.SearchResults {
		url := strings.TrimSpace(item.URL)
		title := strings.TrimSpace(item.Title)
		if url == "" && title == "" {
			continue
		}
		snippet := firstNonEmpty(item.Snippet, item.Summary, item.Content)
		sources = append(sources, Source{
			Title:   title,
			URL:     url,
			Snippet: snippet,
		})
	}
	return sources
}

func buildUserPrompt(question, disclaimer string) string {
	return fmt.Sprintf("用户问题：\n%s\n\n请基于联网搜索结果回答。回答开头必须包含：\n%s", question, disclaimer)
}

func ensureDisclaimer(answer, disclaimer string) string {
	disclaimer = strings.TrimSpace(disclaimer)
	if disclaimer == "" || strings.Contains(answer, disclaimer) {
		return answer
	}
	return disclaimer + "\n\n" + answer
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func contentText(content any) string {
	switch value := content.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		var builder strings.Builder
		for _, item := range value {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, ok := part["text"].(string)
			if !ok {
				continue
			}
			builder.WriteString(text)
		}
		return strings.TrimSpace(builder.String())
	default:
		return ""
	}
}

const webFallbackSystemPrompt = `你是联网搜索降级回答助手。
当前用户的知识库没有找到足够信息，你可以基于联网搜索结果回答。
回答必须清楚说明信息来自网络搜索，仅供参考。
不要声称这些信息来自用户知识库。
如果网络结果不足或不确定，请直接说明不确定。`
