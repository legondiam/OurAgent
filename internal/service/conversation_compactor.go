package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"OurAgent/internal/agent"
	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"gorm.io/datatypes"
)

const conversationSummarySystemPrompt = `你是企业Agent的会话摘要器。
请把旧摘要和新增问答合并成一个完整的结构化会话摘要。

要求：
1. 输出JSON，不要输出Markdown代码块或其他文字
2. schema_version固定为1
3. 支持同一会话存在多个话题，当前问题优先，不得混合无关话题实体
4. 保留用户目标、已确认实体、约束、讨论进度、待确认问题和用户纠正
5. 用户纠正旧值后，只把新值作为当前有效实体
6. discussion_results必须附带真实source_chat_log_ids
7. 会话摘要不是企业事实来源，不要补充输入中不存在的事实
8. 已完成旧话题可以压缩，进行中或存在待确认项的话题优先保留`

type ConversationCompactor struct {
	conversations *repository.ConversationRepository
	logs          *repository.ChatLogRepository
	chat          einomodel.BaseChatModel
	cfg           config.AgentMemoryConfig
}

func NewConversationCompactor(conversations *repository.ConversationRepository, logs *repository.ChatLogRepository, chat einomodel.BaseChatModel, cfg config.AgentMemoryConfig) *ConversationCompactor {
	return &ConversationCompactor{conversations: conversations, logs: logs, chat: chat, cfg: cfg}
}

// Compact执行一次固定快照范围的增量会话压缩
func (c *ConversationCompactor) Compact(ctx context.Context, task repository.ConversationCompactionTask) error {
	now := time.Now()
	leaseSeconds := c.cfg.CompactionLeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = 180
	}
	claimed, err := c.conversations.ClaimCompaction(task, task.Attempt, now, now.Add(time.Duration(leaseSeconds)*time.Second))
	if err != nil {
		return err
	}
	if !claimed {
		conversation, findErr := c.conversations.FindOwned(task.UserID, task.KnowledgeBaseID, task.ConversationID)
		if findErr == nil && conversation.SummaryTaskID == task.TaskID && conversation.SummaryStatus == model.ConversationSummaryStatusProcessing && conversation.SummaryLeaseUntil != nil && conversation.SummaryLeaseUntil.After(now) {
			return repository.ErrConversationSummaryLeaseActive
		}
		return nil
	}
	conversation, err := c.conversations.FindOwned(task.UserID, task.KnowledgeBaseID, task.ConversationID)
	if err != nil {
		return err
	}
	if conversation.Status != model.ConversationStatusActive || !conversation.ExpiresAt.After(now) {
		return c.conversations.FailCompaction(task.ConversationID, task.TaskID, task.Attempt, "会话已过期")
	}
	logs, err := c.logs.ListRangeByConversation(task.UserID, task.KnowledgeBaseID, task.ConversationID, conversation.SummarizedThroughID, task.SnapshotLastLogID)
	if err != nil {
		return err
	}
	if len(logs) == 0 {
		return c.conversations.FailCompaction(task.ConversationID, task.TaskID, task.Attempt, "没有可压缩的会话日志")
	}
	compactLogs := selectCompactLogs(logs, c.cfg.KeepRecentTokens)
	if len(compactLogs) == 0 {
		compactLogs = logs
	}
	input := buildConversationSummaryInput(conversation.SummaryJSON, compactLogs)
	timeout := c.cfg.SummaryTimeoutSeconds
	if timeout <= 0 {
		timeout = 120
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	resp, err := c.chat.Generate(callCtx, []*schema.Message{
		schema.SystemMessage(conversationSummarySystemPrompt),
		schema.UserMessage(input),
	})
	if err != nil {
		return err
	}
	summaryJSON := extractJSON(resp.Content)
	summary, err := agent.ParseConversationSummary([]byte(summaryJSON))
	if err != nil {
		return fmt.Errorf("解析会话摘要失败: %w", err)
	}
	if err := c.validateSummary(task, summary); err != nil {
		return err
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	maxTokens := c.cfg.SummaryTargetTokens
	if maxTokens <= 0 {
		maxTokens = 1000
	}
	if agent.EstimateChatTokens(string(raw)) > maxTokens*2 {
		return fmt.Errorf("会话摘要超过长度上限")
	}
	compactedTokens := 0
	for _, log := range compactLogs {
		compactedTokens += conversationLogTokens(log)
	}
	return c.conversations.CompleteCompaction(task, datatypes.JSON(raw), compactLogs[len(compactLogs)-1].ID, compactedTokens)
}

func selectCompactLogs(logs []model.ChatLog, keepRecentTokens int) []model.ChatLog {
	if len(logs) == 0 {
		return nil
	}
	if keepRecentTokens <= 0 {
		keepRecentTokens = 2000
	}
	tailStart := len(logs)
	tailTokens := 0
	for i := len(logs) - 1; i >= 0; i-- {
		tokens := conversationLogTokens(logs[i])
		if tailStart < len(logs) && tailTokens+tokens > keepRecentTokens {
			break
		}
		tailTokens += tokens
		tailStart = i
	}
	if tailStart == 0 {
		return nil
	}
	return logs[:tailStart]
}

func buildConversationSummaryInput(oldSummary []byte, logs []model.ChatLog) string {
	var b strings.Builder
	b.WriteString("旧摘要JSON：\n")
	if len(oldSummary) == 0 {
		b.WriteString(`{"schema_version":1,"active_topic":"","topics":[]}`)
	} else {
		b.Write(oldSummary)
	}
	b.WriteString("\n\n需要合并的问答：\n")
	for _, log := range logs {
		b.WriteString("\nchat_log_id=")
		b.WriteString(uintString(log.ID))
		b.WriteString("\nanswer_mode=")
		b.WriteString(log.AnswerMode)
		b.WriteString("\n用户：")
		b.WriteString(log.Question)
		b.WriteString("\n助手：")
		b.WriteString(log.Answer)
		b.WriteString("\n")
	}
	return b.String()
}

func (c *ConversationCompactor) validateSummary(task repository.ConversationCompactionTask, summary agent.ConversationSummary) error {
	if summary.SchemaVersion != 1 {
		return fmt.Errorf("不支持的会话摘要版本: %d", summary.SchemaVersion)
	}
	ids := make([]uint64, 0)
	for _, topic := range summary.Topics {
		for _, result := range topic.DiscussionResults {
			ids = append(ids, result.SourceChatLogIDs...)
		}
		for _, correction := range topic.UserCorrections {
			if correction.SourceChatLogID != 0 {
				ids = append(ids, correction.SourceChatLogID)
			}
		}
	}
	ids = uniqueUint64(ids, 100)
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if id > task.SnapshotLastLogID {
			return fmt.Errorf("摘要引用了快照范围外的会话日志")
		}
	}
	logs, err := c.logs.FindManyOwnedByConversation(task.UserID, task.KnowledgeBaseID, task.ConversationID, ids)
	if err != nil {
		return err
	}
	if len(logs) != len(ids) {
		return fmt.Errorf("摘要引用了不属于当前会话的日志")
	}
	return nil
}

func extractJSON(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		return content[start : end+1]
	}
	return content
}
