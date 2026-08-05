package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const memoryDirectivePrompt = `解析用户是否明确要求管理长期记忆，只输出JSON：
{"operation":"remember|correct|forget|none","type":"role|preference|business_object|project_context|terminology|instruction","scope":"knowledge_base|user_global","subject":"","attribute":"","value":"","content":"","durability":"temporary|persistent","evidence_excerpt":"","residual_question":""}
规则：
1. 普通陈述返回none，只有明确的记住、纠正既有记忆或忘掉指令才返回操作
2. 产品能力、套餐、价格、制度、权限、API参数、版本能力等企业事实，即使要求记住也返回none
3. 密码、Token、密钥、身份号、银行卡、客户原始数据以及绕过审批权限审计的指令返回none
4. user_global只允许用户明确确认的preference，其余固定knowledge_base
5. evidence_excerpt必须是用户输入中的连续原文
6. residual_question只保留记忆指令之外仍需回答的问题`

type MemoryDirectiveResult struct {
	Operation        *model.PendingMemoryOperation
	ResidualQuestion string
	Confirmation     string
	Handled          bool
}

type MemoryDirectiveProcessor struct {
	chat einomodel.BaseChatModel
	cfg  config.LongTermMemoryConfig
}

// NewMemoryDirectiveProcessor 创建显式记忆指令处理器
func NewMemoryDirectiveProcessor(chat einomodel.BaseChatModel, cfg config.LongTermMemoryConfig) *MemoryDirectiveProcessor {
	return &MemoryDirectiveProcessor{chat: chat, cfg: cfg}
}

// Process 解析并校验显式记住、纠正和遗忘指令
func (p *MemoryDirectiveProcessor) Process(ctx context.Context, text string) (MemoryDirectiveResult, error) {
	if p.chat == nil {
		return MemoryDirectiveResult{}, fmt.Errorf("记忆指令模型未配置")
	}
	timeout := p.cfg.DirectiveTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	response, err := p.chat.Generate(callCtx, []*schema.Message{schema.SystemMessage(memoryDirectivePrompt), schema.UserMessage(text)})
	if err != nil {
		return MemoryDirectiveResult{}, err
	}
	var raw struct {
		Operation        string `json:"operation"`
		Type             string `json:"type"`
		Scope            string `json:"scope"`
		Subject          string `json:"subject"`
		Attribute        string `json:"attribute"`
		Value            string `json:"value"`
		Content          string `json:"content"`
		Durability       string `json:"durability"`
		EvidenceExcerpt  string `json:"evidence_excerpt"`
		ResidualQuestion string `json:"residual_question"`
	}
	if err := json.Unmarshal([]byte(extractJSON(response.Content)), &raw); err != nil {
		return MemoryDirectiveResult{}, fmt.Errorf("解析记忆指令失败: %w", err)
	}
	raw.Operation = strings.ToLower(strings.TrimSpace(raw.Operation))
	if raw.Operation == "none" || raw.Operation == "" {
		return MemoryDirectiveResult{ResidualQuestion: strings.TrimSpace(raw.ResidualQuestion)}, nil
	}
	if raw.Operation != "remember" && raw.Operation != "correct" && raw.Operation != "forget" {
		return MemoryDirectiveResult{}, fmt.Errorf("不支持的记忆操作")
	}
	if !strings.Contains(text, raw.EvidenceExcerpt) || strings.TrimSpace(raw.EvidenceExcerpt) == "" {
		return MemoryDirectiveResult{}, fmt.Errorf("记忆指令证据无法归因")
	}
	operation := &model.PendingMemoryOperation{Kind: raw.Operation, Type: strings.TrimSpace(raw.Type), Scope: strings.TrimSpace(raw.Scope), Subject: strings.TrimSpace(raw.Subject), Attribute: strings.TrimSpace(raw.Attribute), Value: strings.TrimSpace(raw.Value), Content: strings.TrimSpace(raw.Content), Durability: strings.TrimSpace(raw.Durability), EvidenceExcerpt: raw.EvidenceExcerpt, Explicit: true}
	if operation.Scope == "" {
		operation.Scope = model.MemoryScopeKnowledgeBase
	}
	if operation.Durability == "" {
		operation.Durability = "temporary"
	}
	if operation.Kind != "forget" {
		candidate := memoryCandidateOutput{Type: operation.Type, Subject: operation.Subject, Attribute: operation.Attribute, Value: operation.Value, Content: operation.Content, Durability: operation.Durability, EvidenceExcerpt: operation.EvidenceExcerpt}
		if !allowedMemoryCandidate(candidate) {
			return MemoryDirectiveResult{Handled: true, Confirmation: "这类企业事实或敏感内容不属于长期记忆，未保存。"}, nil
		}
		if !validMemoryTypeScope(operation.Type, operation.Scope) {
			return MemoryDirectiveResult{Handled: true, Confirmation: "回答偏好只能保存为全局记忆，其他用户背景只能保存在当前知识库，本次未保存。"}, nil
		}
	} else if operation.Subject == "" {
		return MemoryDirectiveResult{Handled: true, Confirmation: "请说明要忘掉的具体长期记忆；批量删除请使用记忆管理入口。"}, nil
	}
	confirmation := "已记住这项用户背景。"
	if operation.Kind == "correct" {
		confirmation = "已按你的说明更新这项记忆。"
	}
	if operation.Kind == "forget" {
		confirmation = "已停止使用相关记忆，后台正在彻底删除；原始聊天记录不会被删除。"
	}
	return MemoryDirectiveResult{Operation: operation, ResidualQuestion: strings.TrimSpace(raw.ResidualQuestion), Confirmation: confirmation, Handled: true}, nil
}
