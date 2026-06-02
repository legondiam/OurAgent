package einoapp

import (
	"context"
	"errors"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
)

func NewChatModel(ctx context.Context, baseURL, apiKey, modelName string) (einomodel.BaseChatModel, error) {
	return NewChatModelWithTemperature(ctx, baseURL, apiKey, modelName, 0.2)
}

func NewChatModelWithTemperature(ctx context.Context, baseURL, apiKey, modelName string, temperature float64) (einomodel.BaseChatModel, error) {
	if apiKey == "" {
		return nil, errors.New("模型APIKey不能为空")
	}
	temp := float32(temperature)
	return einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Model:       modelName,
		Temperature: &temp,
		Timeout:     120 * time.Second,
	})
}
