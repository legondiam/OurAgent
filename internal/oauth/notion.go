package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/repository"
	appsource "OurAgent/internal/source"

	"github.com/golang-jwt/jwt/v5"
)

const (
	notionAuthorizeURL = "https://api.notion.com/v1/oauth/authorize"
	notionTokenURL     = "https://api.notion.com/v1/oauth/token"
)

type NotionService struct {
	cfg     config.NotionOAuthConfig
	secret  string
	sources *repository.SourceRepository
	client  *http.Client
}

type notionStateClaims struct {
	SourceID uint64 `json:"source_id"`
	UserID   uint64 `json:"user_id"`
	Nonce    string `json:"nonce"`
	jwt.RegisteredClaims
}

func NewNotionService(cfg config.NotionOAuthConfig, jwtSecret string, sources *repository.SourceRepository) *NotionService {
	return &NotionService{
		cfg:     cfg,
		secret:  jwtSecret,
		sources: sources,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *NotionService) AuthorizeURL(ctx context.Context, userID, sourceID uint64) (string, error) {
	if err := s.ensureConfig(); err != nil {
		return "", err
	}
	source, err := s.sources.FindSourceByIDAndUserID(sourceID, userID)
	if err != nil {
		return "", err
	}
	if source.Provider != appsource.ProviderNotion {
		return "", fmt.Errorf("knowledge source provider is not notion")
	}
	state, err := s.signState(sourceID, userID)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Set("client_id", s.cfg.ClientID)
	values.Set("response_type", "code")
	values.Set("owner", "user")
	values.Set("redirect_uri", s.cfg.RedirectURL)
	values.Set("state", state)
	_ = ctx
	return notionAuthorizeURL + "?" + values.Encode(), nil
}

func (s *NotionService) HandleCallback(ctx context.Context, code, state string) (*CallbackResult, error) {
	if err := s.ensureConfig(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("notion oauth code is empty")
	}
	claims, err := s.verifyState(state)
	if err != nil {
		return nil, err
	}
	token, err := s.exchangeToken(ctx, strings.TrimSpace(code))
	if err != nil {
		return nil, err
	}
	credential, err := json.Marshal(token)
	if err != nil {
		return nil, err
	}
	if err := s.sources.UpdateSourceCredential(claims.SourceID, claims.UserID, credential); err != nil {
		return nil, err
	}
	return &CallbackResult{
		SourceID:      claims.SourceID,
		UserID:        claims.UserID,
		WorkspaceID:   token.WorkspaceID,
		WorkspaceName: token.WorkspaceName,
	}, nil
}

type CallbackResult struct {
	SourceID      uint64 `json:"source_id"`
	UserID        uint64 `json:"user_id"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
}

type notionTokenResponse struct {
	AccessToken          string          `json:"access_token"`
	BotID                string          `json:"bot_id"`
	DuplicatedTemplateID string          `json:"duplicated_template_id"`
	Owner                json.RawMessage `json:"owner"`
	WorkspaceIcon        string          `json:"workspace_icon"`
	WorkspaceID          string          `json:"workspace_id"`
	WorkspaceName        string          `json:"workspace_name"`
}

func (s *NotionService) exchangeToken(ctx context.Context, code string) (*notionTokenResponse, error) {
	body := map[string]string{
		"grant_type":   "authorization_code",
		"code":         code,
		"redirect_uri": s.cfg.RedirectURL,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, notionTokenURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.cfg.ClientID+":"+s.cfg.ClientSecret)))
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("notion oauth token失败: status=%d body=%s", resp.StatusCode, string(respBody))
	}
	var token notionTokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("notion oauth access_token is empty")
	}
	return &token, nil
}

func (s *NotionService) signState(sourceID, userID uint64) (string, error) {
	now := time.Now()
	claims := notionStateClaims{
		SourceID: sourceID,
		UserID:   userID,
		Nonce:    fmt.Sprintf("%d", now.UnixNano()),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.secret))
}

func (s *NotionService) verifyState(state string) (*notionStateClaims, error) {
	claims := &notionStateClaims{}
	token, err := jwt.ParseWithClaims(state, claims, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("invalid oauth state signing method")
		}
		return []byte(s.secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid || claims.SourceID == 0 || claims.UserID == 0 {
		return nil, fmt.Errorf("invalid oauth state")
	}
	return claims, nil
}

func (s *NotionService) ensureConfig() error {
	if strings.TrimSpace(s.cfg.ClientID) == "" {
		return fmt.Errorf("notion oauth client_id is empty")
	}
	if strings.TrimSpace(s.cfg.ClientSecret) == "" {
		return fmt.Errorf("notion oauth client_secret is empty")
	}
	if strings.TrimSpace(s.cfg.RedirectURL) == "" {
		return fmt.Errorf("notion oauth redirect_url is empty")
	}
	return nil
}
