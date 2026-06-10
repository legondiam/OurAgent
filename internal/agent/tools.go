package agent

import (
	"context"
	"encoding/json"
)

// ToolInput表示Agent工具输入
type ToolInput struct {
	UserID          uint64          `json:"user_id"`
	KnowledgeBaseID uint64          `json:"knowledge_base_id"`
	Question        string          `json:"question"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

// ToolResult表示Agent工具输出
type ToolResult struct {
	Answer   string         `json:"answer,omitempty"`
	Sources  any            `json:"sources,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Tool定义Agent可调用工具
type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, input ToolInput) (ToolResult, error)
}
