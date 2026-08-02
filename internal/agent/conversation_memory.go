package agent

import (
	"encoding/json"
	"strconv"
	"strings"
)

const conversationMessageTokenOverhead = 8

type ConversationSummary struct {
	SchemaVersion int                 `json:"schema_version"`
	ActiveTopic   string              `json:"active_topic"`
	Topics        []ConversationTopic `json:"topics"`
}

type ConversationTopic struct {
	Key                  string               `json:"key"`
	Title                string               `json:"title"`
	Status               string               `json:"status"`
	Overview             string               `json:"overview"`
	UserGoal             string               `json:"user_goal"`
	Entities             []ConversationEntity `json:"entities"`
	ConfirmedConstraints []string             `json:"confirmed_constraints"`
	DiscussionResults    []DiscussionResult   `json:"discussion_results"`
	PendingQuestions     []string             `json:"pending_questions"`
	UserCorrections      []UserCorrection     `json:"user_corrections"`
}

type ConversationEntity struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DiscussionResult struct {
	Content          string   `json:"content"`
	SourceChatLogIDs []uint64 `json:"source_chat_log_ids"`
}

type UserCorrection struct {
	OldValue        string `json:"old_value"`
	NewValue        string `json:"new_value"`
	SourceChatLogID uint64 `json:"source_chat_log_id"`
}

// EstimateChatTokens保守估算会话文本Token数
func EstimateChatTokens(text string) int {
	tokens := 0
	asciiRunes := 0
	for _, r := range text {
		if r <= 127 {
			asciiRunes++
			continue
		}
		tokens++
	}
	tokens += (asciiRunes + 3) / 4
	return tokens
}

// EstimateConversationTurnTokens估算完整问答轮次Token数
func EstimateConversationTurnTokens(question, answer string) int {
	return EstimateChatTokens(question) + EstimateChatTokens(answer) + conversationMessageTokenOverhead
}

// ParseConversationSummary解析并校验会话摘要
func ParseConversationSummary(data []byte) (ConversationSummary, error) {
	var summary ConversationSummary
	if len(data) == 0 {
		return summary, nil
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		return ConversationSummary{}, err
	}
	if summary.SchemaVersion == 0 {
		summary.SchemaVersion = 1
	}
	return summary, nil
}

// RenderConversationSummary将结构化摘要渲染为Planner文本
func RenderConversationSummary(summary ConversationSummary) string {
	if len(summary.Topics) == 0 {
		return ""
	}
	var b strings.Builder
	for _, topic := range summary.Topics {
		b.WriteString("话题：")
		b.WriteString(topic.Title)
		if topic.Key == summary.ActiveTopic {
			b.WriteString("（当前）")
		}
		b.WriteString("\n")
		if topic.Overview != "" {
			b.WriteString("概述：")
			b.WriteString(topic.Overview)
			b.WriteString("\n")
		}
		if topic.UserGoal != "" {
			b.WriteString("用户目标：")
			b.WriteString(topic.UserGoal)
			b.WriteString("\n")
		}
		for _, entity := range topic.Entities {
			b.WriteString("实体：")
			b.WriteString(entity.Type)
			b.WriteString("=")
			b.WriteString(entity.Value)
			b.WriteString("\n")
		}
		for _, constraint := range topic.ConfirmedConstraints {
			b.WriteString("已确认约束：")
			b.WriteString(constraint)
			b.WriteString("\n")
		}
		for _, result := range topic.DiscussionResults {
			b.WriteString("讨论结果：")
			b.WriteString(result.Content)
			if len(result.SourceChatLogIDs) > 0 {
				b.WriteString("（来源chat_log_id=")
				for i, id := range result.SourceChatLogIDs {
					if i > 0 {
						b.WriteString(",")
					}
					b.WriteString(strconv.FormatUint(id, 10))
				}
				b.WriteString("）")
			}
			b.WriteString("\n")
		}
		for _, pending := range topic.PendingQuestions {
			b.WriteString("待确认：")
			b.WriteString(pending)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}
