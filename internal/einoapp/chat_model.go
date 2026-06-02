package einoapp

import (
	"context"
	"errors"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
)

func NewChatModel(ctx context.Context, baseURL, apiKey, modelName string) (einomodel.BaseChatModel, error) {
	if apiKey == "" {
		return nil, errors.New("模型APIKey不能为空")
	}
	temperature := float32(0.2)
	return einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Model:       modelName,
		Temperature: &temperature,
		Timeout:     120 * time.Second,
	})
}
