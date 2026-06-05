package source

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/datatypes"
)

const (
	ProviderManual = "manual"
	ProviderNotion = "notion"
	ProviderFeishu = "feishu"
)

type Connector interface {
	List(ctx context.Context, req ListRequest) (*ListResult, error)
	Fetch(ctx context.Context, req FetchRequest) (*RemoteDocument, error)
}

type ListRequest struct {
	Config     datatypes.JSON
	Credential datatypes.JSON
}

type FetchRequest struct {
	Config     datatypes.JSON
	Credential datatypes.JSON
	RemoteID   string
}

type ListResult struct {
	Items []RemoteItem
}

type RemoteItem struct {
	RemoteID    string
	Title       string
	URL         string
	UpdatedAt   time.Time
	ContentHash string
	AlwaysFetch bool
}

type RemoteDocument struct {
	RemoteID    string
	Title       string
	URL         string
	UpdatedAt   time.Time
	Markdown    string
	ContentHash string
}

func NewConnector(provider string) (Connector, error) {
	switch provider {
	case ProviderManual:
		return ManualConnector{}, nil
	case ProviderNotion:
		return NewNotionConnector(), nil
	case ProviderFeishu:
		return NewFeishuConnector(), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

type ManualConnector struct{}

type manualConfig struct {
	Documents []manualDocument `json:"documents"`
}

type manualDocument struct {
	RemoteID  string `json:"remote_id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updated_at"`
	Markdown  string `json:"markdown"`
}

func (ManualConnector) List(ctx context.Context, req ListRequest) (*ListResult, error) {
	cfg, err := parseManualConfig(req.Config)
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
		remote, err := doc.toRemoteDocument()
		if err != nil {
			return nil, err
		}
		items = append(items, RemoteItem{
			RemoteID:    remote.RemoteID,
			Title:       remote.Title,
			URL:         remote.URL,
			UpdatedAt:   remote.UpdatedAt,
			ContentHash: remote.ContentHash,
		})
	}
	return &ListResult{Items: items}, nil
}

func (ManualConnector) Fetch(ctx context.Context, req FetchRequest) (*RemoteDocument, error) {
	cfg, err := parseManualConfig(req.Config)
	if err != nil {
		return nil, err
	}
	for _, doc := range cfg.Documents {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if doc.RemoteID != req.RemoteID {
			continue
		}
		return doc.toRemoteDocument()
	}
	return nil, fmt.Errorf("remote document not found: %s", req.RemoteID)
}

func parseManualConfig(data datatypes.JSON) (*manualConfig, error) {
	var cfg manualConfig
	if len(data) == 0 {
		return &cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid manual provider config: %w", err)
	}
	return &cfg, nil
}

func (d manualDocument) toRemoteDocument() (*RemoteDocument, error) {
	updatedAt, err := parseRemoteTime(d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if d.RemoteID == "" {
		return nil, fmt.Errorf("manual document remote_id is empty")
	}
	title := d.Title
	if title == "" {
		title = d.RemoteID
	}
	markdown := d.Markdown
	hash := HashContent(markdown)
	return &RemoteDocument{
		RemoteID:    d.RemoteID,
		Title:       title,
		URL:         d.URL,
		UpdatedAt:   updatedAt,
		Markdown:    markdown,
		ContentHash: hash,
	}, nil
}

func parseRemoteTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return t, nil
	}
	t, err = time.Parse("2006-01-02 15:04:05", value)
	if err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid updated_at: %s", value)
}
