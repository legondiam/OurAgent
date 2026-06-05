package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/datatypes"
)

const (
	defaultNotionBaseURL = "https://api.notion.com"
	defaultNotionVersion = "2026-03-11"
)

type NotionConnector struct {
	client *http.Client
}

type notionConfig struct {
	BaseURL           string       `json:"base_url"`
	NotionVersion     string       `json:"notion_version"`
	IncludeTranscript bool         `json:"include_transcript"`
	Pages             []notionPage `json:"pages"`
	DatabaseID        string       `json:"database_id"`
	PageSize          int          `json:"page_size"`
}

type notionPage struct {
	PageID    string `json:"page_id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updated_at"`
}

type notionCredential struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func NewNotionConnector() *NotionConnector {
	return &NotionConnector{client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *NotionConnector) List(ctx context.Context, req ListRequest) (*ListResult, error) {
	cfg, err := parseNotionConfig(req.Config)
	if err != nil {
		return nil, err
	}
	items := make([]RemoteItem, 0, len(cfg.Pages))
	for _, page := range cfg.Pages {
		item, err := page.toRemoteItem()
		if err != nil {
			return nil, err
		}
		meta, err := c.retrievePage(ctx, cfg, req.Credential, item.RemoteID)
		if err == nil {
			item.Title = firstNonEmpty(item.Title, meta.Title)
			item.URL = firstNonEmpty(item.URL, meta.URL)
			item.UpdatedAt = meta.UpdatedAt
		}
		if item.UpdatedAt.IsZero() && item.ContentHash == "" {
			item.AlwaysFetch = true
		}
		items = append(items, item)
	}
	if strings.TrimSpace(cfg.DatabaseID) == "" {
		return &ListResult{Items: items}, nil
	}
	dbItems, err := c.listDatabasePages(ctx, cfg, req.Credential)
	if err != nil {
		return nil, err
	}
	items = append(items, dbItems...)
	return &ListResult{Items: items}, nil
}

func (c *NotionConnector) Fetch(ctx context.Context, req FetchRequest) (*RemoteDocument, error) {
	cfg, err := parseNotionConfig(req.Config)
	if err != nil {
		return nil, err
	}
	page, _ := findNotionPage(cfg.Pages, req.RemoteID)
	meta, err := c.retrievePage(ctx, cfg, req.Credential, req.RemoteID)
	if err != nil {
		return nil, err
	}
	markdown, err := c.retrieveMarkdown(ctx, cfg, req.Credential, req.RemoteID)
	if err != nil {
		return nil, err
	}
	title := firstNonEmpty(page.Title, meta.Title, req.RemoteID)
	url := firstNonEmpty(page.URL, meta.URL)
	updatedAt := meta.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt, _ = parseRemoteTime(page.UpdatedAt)
	}
	return &RemoteDocument{
		RemoteID:    req.RemoteID,
		Title:       title,
		URL:         url,
		UpdatedAt:   updatedAt,
		Markdown:    markdown,
		ContentHash: HashContent(markdown),
	}, nil
}

func (c *NotionConnector) listDatabasePages(ctx context.Context, cfg *notionConfig, credential datatypes.JSON) ([]RemoteItem, error) {
	pageSize := cfg.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	var items []RemoteItem
	var cursor string
	for {
		body := map[string]interface{}{"page_size": pageSize}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		var resp notionDatabaseQueryResponse
		path := fmt.Sprintf("/v1/databases/%s/query", strings.TrimSpace(cfg.DatabaseID))
		if err := c.doJSON(ctx, cfg, credential, http.MethodPost, path, body, &resp); err != nil {
			return nil, err
		}
		for _, page := range resp.Results {
			items = append(items, RemoteItem{
				RemoteID:  page.ID,
				Title:     page.extractTitle(),
				URL:       page.URL,
				UpdatedAt: page.parseLastEditedTime(),
			})
		}
		if !resp.HasMore || strings.TrimSpace(resp.NextCursor) == "" {
			break
		}
		cursor = resp.NextCursor
	}
	return items, nil
}

func (c *NotionConnector) retrievePage(ctx context.Context, cfg *notionConfig, credential datatypes.JSON, pageID string) (*notionPageMeta, error) {
	var page notionPageResponse
	if err := c.doJSON(ctx, cfg, credential, http.MethodGet, "/v1/pages/"+pageID, nil, &page); err != nil {
		return nil, err
	}
	return &notionPageMeta{
		Title:     page.extractTitle(),
		URL:       page.URL,
		UpdatedAt: page.parseLastEditedTime(),
	}, nil
}

func (c *NotionConnector) retrieveMarkdown(ctx context.Context, cfg *notionConfig, credential datatypes.JSON, pageID string) (string, error) {
	path := "/v1/pages/" + pageID + "/markdown"
	if cfg.IncludeTranscript {
		path += "?include_transcript=true"
	}
	var resp struct {
		Markdown string `json:"markdown"`
	}
	if err := c.doJSON(ctx, cfg, credential, http.MethodGet, path, nil, &resp); err != nil {
		return "", err
	}
	return resp.Markdown, nil
}

func (c *NotionConnector) doJSON(ctx context.Context, cfg *notionConfig, credential datatypes.JSON, method, path string, body any, out any) error {
	cred, err := parseNotionCredential(credential)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.baseURL(), "/")+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+cred.Token)
	request.Header.Set("Notion-Version", cfg.version())
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("notion API失败: status=%d body=%s", response.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func parseNotionConfig(data datatypes.JSON) (*notionConfig, error) {
	var cfg notionConfig
	if len(data) == 0 {
		return &cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid notion provider config: %w", err)
	}
	return &cfg, nil
}

func parseNotionCredential(data datatypes.JSON) (*notionCredential, error) {
	var cred notionCredential
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cred); err != nil {
			return nil, fmt.Errorf("invalid notion credential: %w", err)
		}
	}
	cred.Token = strings.TrimSpace(firstNonEmpty(cred.Token, cred.AccessToken))
	if strings.TrimSpace(cred.Token) == "" {
		return nil, fmt.Errorf("notion credential token is empty")
	}
	return &cred, nil
}

func (c *notionConfig) baseURL() string {
	if strings.TrimSpace(c.BaseURL) == "" {
		return defaultNotionBaseURL
	}
	return strings.TrimSpace(c.BaseURL)
}

func (c *notionConfig) version() string {
	if strings.TrimSpace(c.NotionVersion) == "" {
		return defaultNotionVersion
	}
	return strings.TrimSpace(c.NotionVersion)
}

func (p notionPage) toRemoteItem() (RemoteItem, error) {
	updatedAt, err := parseRemoteTime(p.UpdatedAt)
	if err != nil {
		return RemoteItem{}, err
	}
	if strings.TrimSpace(p.PageID) == "" {
		return RemoteItem{}, fmt.Errorf("notion page_id is empty")
	}
	return RemoteItem{
		RemoteID:  strings.TrimSpace(p.PageID),
		Title:     p.Title,
		URL:       p.URL,
		UpdatedAt: updatedAt,
	}, nil
}

func findNotionPage(pages []notionPage, pageID string) (notionPage, bool) {
	for _, page := range pages {
		if strings.TrimSpace(page.PageID) == pageID {
			return page, true
		}
	}
	return notionPage{}, false
}

type notionPageMeta struct {
	Title     string
	URL       string
	UpdatedAt time.Time
}

type notionDatabaseQueryResponse struct {
	Results    []notionPageResponse `json:"results"`
	HasMore    bool                 `json:"has_more"`
	NextCursor string               `json:"next_cursor"`
}

type notionPageResponse struct {
	ID             string                    `json:"id"`
	URL            string                    `json:"url"`
	LastEditedTime string                    `json:"last_edited_time"`
	Properties     map[string]notionProperty `json:"properties"`
}

type notionProperty struct {
	Type  string             `json:"type"`
	Title []notionRichText   `json:"title"`
	Text  []notionRichText   `json:"rich_text"`
	Name  string             `json:"name"`
	Items []notionSelectItem `json:"multi_select"`
}

type notionRichText struct {
	PlainText string `json:"plain_text"`
}

type notionSelectItem struct {
	Name string `json:"name"`
}

func (p notionPageResponse) parseLastEditedTime() time.Time {
	t, _ := parseRemoteTime(p.LastEditedTime)
	return t
}

func (p notionPageResponse) extractTitle() string {
	for _, prop := range p.Properties {
		if prop.Type != "title" {
			continue
		}
		parts := make([]string, 0, len(prop.Title))
		for _, text := range prop.Title {
			if strings.TrimSpace(text.PlainText) != "" {
				parts = append(parts, text.PlainText)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "")
		}
	}
	return p.ID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
