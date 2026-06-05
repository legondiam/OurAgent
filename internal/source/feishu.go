package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/datatypes"
)

const defaultFeishuBaseURL = "https://open.feishu.cn"

type FeishuConnector struct {
	client *http.Client
}

type feishuConfig struct {
	BaseURL   string           `json:"base_url"`
	DocType   string           `json:"doc_type"`
	Lang      string           `json:"lang"`
	Documents []feishuDocument `json:"documents"`
}

type feishuDocument struct {
	DocToken   string `json:"doc_token"`
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	UpdatedAt  string `json:"updated_at"`
}

type feishuCredential struct {
	AccessToken string `json:"access_token"`
	Token       string `json:"token"`
}

func NewFeishuConnector() *FeishuConnector {
	return &FeishuConnector{client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *FeishuConnector) List(ctx context.Context, req ListRequest) (*ListResult, error) {
	cfg, err := parseFeishuConfig(req.Config)
	if err != nil {
		return nil, err
	}
	items := make([]RemoteItem, 0, len(cfg.Documents))
	for _, doc := range cfg.Documents {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		item, err := doc.toRemoteItem()
		if err != nil {
			return nil, err
		}
		if doc.DocumentID != "" {
			meta, err := c.retrieveDocumentMeta(ctx, cfg, req.Credential, doc.DocumentID)
			if err == nil {
				item.Title = firstNonEmpty(item.Title, meta.Title)
				item.UpdatedAt = meta.UpdatedAt
				item.ContentHash = meta.VersionHash
			}
		}
		if item.UpdatedAt.IsZero() && item.ContentHash == "" {
			item.AlwaysFetch = true
		}
		items = append(items, item)
	}
	return &ListResult{Items: items}, nil
}

func (c *FeishuConnector) Fetch(ctx context.Context, req FetchRequest) (*RemoteDocument, error) {
	cfg, err := parseFeishuConfig(req.Config)
	if err != nil {
		return nil, err
	}
	doc, ok := findFeishuDocument(cfg.Documents, req.RemoteID)
	if !ok {
		return nil, fmt.Errorf("feishu document not found: %s", req.RemoteID)
	}
	meta := feishuDocumentMeta{}
	if doc.DocumentID != "" {
		meta, _ = c.retrieveDocumentMeta(ctx, cfg, req.Credential, doc.DocumentID)
	}
	markdown, err := c.retrieveMarkdown(ctx, cfg, req.Credential, doc.remoteID())
	if err != nil {
		return nil, err
	}
	updatedAt, _ := parseRemoteTime(doc.UpdatedAt)
	if !meta.UpdatedAt.IsZero() {
		updatedAt = meta.UpdatedAt
	}
	title := firstNonEmpty(doc.Title, meta.Title, req.RemoteID)
	contentHash := HashContent(markdown)
	if meta.VersionHash != "" {
		contentHash = meta.VersionHash + ":" + contentHash
	}
	return &RemoteDocument{
		RemoteID:    req.RemoteID,
		Title:       title,
		URL:         doc.URL,
		UpdatedAt:   updatedAt,
		Markdown:    markdown,
		ContentHash: contentHash,
	}, nil
}

func (c *FeishuConnector) retrieveMarkdown(ctx context.Context, cfg *feishuConfig, credential datatypes.JSON, docToken string) (string, error) {
	query := url.Values{}
	query.Set("doc_token", docToken)
	query.Set("doc_type", cfg.docType())
	query.Set("content_type", "markdown")
	if cfg.lang() != "" {
		query.Set("lang", cfg.lang())
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Content  string `json:"content"`
			Markdown string `json:"markdown"`
		} `json:"data"`
	}
	path := "/open-apis/docs/v1/content?" + query.Encode()
	if err := c.doJSON(ctx, cfg, credential, http.MethodGet, path, &resp); err != nil {
		return "", err
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("feishu API失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	markdown := firstNonEmpty(resp.Data.Content, resp.Data.Markdown)
	return markdown, nil
}

func (c *FeishuConnector) retrieveDocumentMeta(ctx context.Context, cfg *feishuConfig, credential datatypes.JSON, documentID string) (feishuDocumentMeta, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Document struct {
				Title           string `json:"title"`
				RevisionID      string `json:"revision_id"`
				LatestVersion   string `json:"latest_version"`
				UpdateTime      string `json:"update_time"`
				LastModified    string `json:"last_modified_time"`
				LastModifiedSec int64  `json:"last_modified_time_sec"`
			} `json:"document"`
		} `json:"data"`
	}
	path := "/open-apis/docx/v1/documents/" + documentID
	if err := c.doJSON(ctx, cfg, credential, http.MethodGet, path, &resp); err != nil {
		return feishuDocumentMeta{}, err
	}
	if resp.Code != 0 {
		return feishuDocumentMeta{}, fmt.Errorf("feishu API失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	updatedAt := parseFeishuMetaTime(resp.Data.Document.UpdateTime, resp.Data.Document.LastModified, resp.Data.Document.LastModifiedSec)
	hashSeed := firstNonEmpty(resp.Data.Document.RevisionID, resp.Data.Document.LatestVersion)
	return feishuDocumentMeta{
		Title:       resp.Data.Document.Title,
		UpdatedAt:   updatedAt,
		VersionHash: hashSeed,
		Description: resp.Data.Document.RevisionID,
	}, nil
}

func (c *FeishuConnector) doJSON(ctx context.Context, cfg *feishuConfig, credential datatypes.JSON, method, path string, out any) error {
	cred, err := parseFeishuCredential(credential)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.baseURL(), "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+cred.accessToken())
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
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
		return fmt.Errorf("feishu API失败: status=%d body=%s", response.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func parseFeishuConfig(data datatypes.JSON) (*feishuConfig, error) {
	var cfg feishuConfig
	if len(data) == 0 {
		return &cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid feishu provider config: %w", err)
	}
	return &cfg, nil
}

func parseFeishuCredential(data datatypes.JSON) (*feishuCredential, error) {
	var cred feishuCredential
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cred); err != nil {
			return nil, fmt.Errorf("invalid feishu credential: %w", err)
		}
	}
	if strings.TrimSpace(cred.accessToken()) == "" {
		return nil, fmt.Errorf("feishu credential access_token is empty")
	}
	return &cred, nil
}

func (c *feishuConfig) baseURL() string {
	if strings.TrimSpace(c.BaseURL) == "" {
		return defaultFeishuBaseURL
	}
	return strings.TrimSpace(c.BaseURL)
}

func (c *feishuConfig) docType() string {
	if strings.TrimSpace(c.DocType) == "" {
		return "docx"
	}
	return strings.TrimSpace(c.DocType)
}

func (c *feishuConfig) lang() string {
	if strings.TrimSpace(c.Lang) == "" {
		return "zh"
	}
	return strings.TrimSpace(c.Lang)
}

func (c *feishuCredential) accessToken() string {
	return strings.TrimSpace(firstNonEmpty(c.AccessToken, c.Token))
}

func (d feishuDocument) toRemoteItem() (RemoteItem, error) {
	updatedAt, err := parseRemoteTime(d.UpdatedAt)
	if err != nil {
		return RemoteItem{}, err
	}
	remoteID := d.remoteID()
	if remoteID == "" {
		return RemoteItem{}, fmt.Errorf("feishu document doc_token is empty")
	}
	return RemoteItem{
		RemoteID:  remoteID,
		Title:     d.Title,
		URL:       d.URL,
		UpdatedAt: updatedAt,
	}, nil
}

func (d feishuDocument) remoteID() string {
	return strings.TrimSpace(firstNonEmpty(d.DocToken, d.DocumentID))
}

func findFeishuDocument(docs []feishuDocument, remoteID string) (feishuDocument, bool) {
	for _, doc := range docs {
		if doc.remoteID() == remoteID {
			return doc, true
		}
	}
	return feishuDocument{}, false
}

type feishuDocumentMeta struct {
	Title       string
	UpdatedAt   time.Time
	VersionHash string
	Description string
}

func parseFeishuMetaTime(values ...interface{}) time.Time {
	for _, value := range values {
		switch v := value.(type) {
		case string:
			if t, err := parseRemoteTime(v); err == nil && !t.IsZero() {
				return t
			}
			if timestamp, err := strconv.ParseInt(v, 10, 64); err == nil && timestamp > 0 {
				if timestamp > 1_000_000_000_000 {
					return time.UnixMilli(timestamp)
				}
				return time.Unix(timestamp, 0)
			}
		case int64:
			if v > 0 {
				if v > 1_000_000_000_000 {
					return time.UnixMilli(v)
				}
				return time.Unix(v, 0)
			}
		}
	}
	return time.Time{}
}
