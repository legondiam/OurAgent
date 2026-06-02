package einoapp

import (
	"context"
	"errors"
	"net/http"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/embedding"
)

func NewEmbedding(ctx context.Context, baseURL, apiKey, modelName string) (embedding.Embedder, error) {
	if apiKey == "" {
		return nil, errors.New("模型APIKey不能为空")
	}
	return einoopenai.NewEmbeddingClient(ctx, &einoopenai.EmbeddingConfig{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Model:      modelName,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	})
}
