package service

import (
	"strings"

	"OurAgent/internal/agent"
	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"
)

type ConversationContextAssembler struct {
	conversations *repository.ConversationRepository
	logs          *repository.ChatLogRepository
	cfg           config.AgentMemoryConfig
}

type ContextAssembleRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	ConversationID  string
}

func NewConversationContextAssembler(conversations *repository.ConversationRepository, logs *repository.ChatLogRepository, cfg config.AgentMemoryConfig) *ConversationContextAssembler {
	return &ConversationContextAssembler{conversations: conversations, logs: logs, cfg: cfg}
}

// Build组装受Token预算控制的短期会话上下文
func (a *ConversationContextAssembler) Build(req ContextAssembleRequest) (agent.ConversationContext, error) {
	context := agent.ConversationContext{
		ConversationID: req.ConversationID,
		Messages:       []agent.HistoryMessage{},
	}
	conversation, err := a.conversations.FindOwned(req.UserID, req.KnowledgeBaseID, req.ConversationID)
	if err != nil {
		return context, err
	}
	context.SummarizedThroughID = conversation.SummarizedThroughID
	context.SummaryVersion = conversation.SummaryVersion
	afterID := conversation.SummarizedThroughID
	if len(conversation.SummaryJSON) > 0 {
		summary, parseErr := agent.ParseConversationSummary(conversation.SummaryJSON)
		if parseErr == nil {
			context.Summary = agent.RenderConversationSummary(summary)
		} else {
			context.Degraded = true
			context.DegradedReason = "会话摘要解析失败，回退原始历史"
			afterID = 0
		}
	} else if conversation.SummarizedThroughID > 0 {
		context.Degraded = true
		context.DegradedReason = "会话摘要缺失，回退原始历史"
		afterID = 0
	}
	logs, err := a.logs.ListAfterIDByConversation(req.UserID, req.KnowledgeBaseID, req.ConversationID, afterID)
	if err != nil {
		return context, err
	}
	limit := a.cfg.ContextHardLimitTokens
	if limit <= 0 {
		limit = 6000
	}
	summaryTokens := agent.EstimateChatTokens(context.Summary)
	remaining := limit - summaryTokens
	if remaining < 0 {
		context.Summary = truncateContextText(context.Summary, limit)
		context.EstimatedTokens = agent.EstimateChatTokens(context.Summary)
		context.Degraded = true
		context.DegradedReason = "会话摘要超过上下文硬预算"
		return context, nil
	}
	selected := selectRecentContextLogs(logs, remaining)
	if len(selected) < len(logs) {
		context.Degraded = true
		context.DegradedReason = "异步摘要未完成或增量历史超过上下文硬预算"
	}
	context.EstimatedTokens = summaryTokens
	for _, log := range selected {
		question := strings.TrimSpace(log.Question)
		answer := strings.TrimSpace(log.Answer)
		turnTokens := conversationLogTokens(log)
		if turnTokens > remaining && len(selected) == 1 {
			contentBudget := remaining - 8
			if contentBudget < 0 {
				contentBudget = 0
			}
			questionBudget := agent.EstimateChatTokens(question)
			if questionBudget >= contentBudget {
				question = previewContextAnswer(question, contentBudget)
				answer = ""
			} else {
				answer = previewContextAnswer(answer, contentBudget-questionBudget)
			}
			turnTokens = agent.EstimateConversationTurnTokens(question, answer)
		}
		context.Messages = append(context.Messages, agent.HistoryMessage{
			ChatLogID:  log.ID,
			Question:   question,
			Answer:     answer,
			AnswerMode: log.AnswerMode,
		})
		context.EstimatedTokens += turnTokens
	}
	return context, nil
}

func selectRecentContextLogs(logs []model.ChatLog, budget int) []model.ChatLog {
	if budget <= 0 || len(logs) == 0 {
		return []model.ChatLog{}
	}
	start := len(logs)
	used := 0
	for i := len(logs) - 1; i >= 0; i-- {
		tokens := conversationLogTokens(logs[i])
		if used > 0 && used+tokens > budget {
			break
		}
		used += tokens
		start = i
		if used >= budget {
			break
		}
	}
	return logs[start:]
}

func conversationLogTokens(log model.ChatLog) int {
	if log.ConversationTokens > 0 {
		return log.ConversationTokens
	}
	return agent.EstimateConversationTurnTokens(log.Question, log.Answer)
}

func previewContextAnswer(answer string, tokenBudget int) string {
	if tokenBudget <= 0 {
		return ""
	}
	runes := []rune(answer)
	maxRunes := tokenBudget
	if len(runes) <= maxRunes {
		return answer
	}
	if maxRunes < 20 {
		return string(runes[:maxRunes])
	}
	head := maxRunes * 2 / 3
	tail := maxRunes - head
	return string(runes[:head]) + "\n……\n" + string(runes[len(runes)-tail:])
}

func truncateContextText(text string, tokenBudget int) string {
	return previewContextAnswer(text, tokenBudget)
}
