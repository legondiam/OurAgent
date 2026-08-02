package service

import (
	"strings"
	"testing"

	"OurAgent/internal/agent"
	"OurAgent/internal/model"
)

func TestSelectRecentContextLogsPrioritizesNewestTurns(t *testing.T) {
	logs := []model.ChatLog{
		{ID: 1, ConversationTokens: 100},
		{ID: 2, ConversationTokens: 100},
		{ID: 3, ConversationTokens: 100},
	}
	selected := selectRecentContextLogs(logs, 210)
	if len(selected) != 2 || selected[0].ID != 2 || selected[1].ID != 3 {
		t.Fatalf("expected newest logs 2 and 3, got %+v", selected)
	}
}

func TestSelectRecentContextLogsKeepsOversizedNewestTurn(t *testing.T) {
	logs := []model.ChatLog{{ID: 1, ConversationTokens: 100}, {ID: 2, ConversationTokens: 500}}
	selected := selectRecentContextLogs(logs, 200)
	if len(selected) != 1 || selected[0].ID != 2 {
		t.Fatalf("expected oversized newest log, got %+v", selected)
	}
}

func TestSelectCompactLogsKeepsRecentTokenTail(t *testing.T) {
	logs := []model.ChatLog{
		{ID: 1, ConversationTokens: 1200},
		{ID: 2, ConversationTokens: 1200},
		{ID: 3, ConversationTokens: 900},
	}
	compact := selectCompactLogs(logs, 2000)
	if len(compact) != 2 || compact[0].ID != 1 || compact[1].ID != 2 {
		t.Fatalf("expected old logs 1 and 2 to compact, got %+v", compact)
	}
}

func TestPreviewContextAnswerKeepsHeadAndTail(t *testing.T) {
	answer := strings.Repeat("前", 60) + strings.Repeat("后", 40)
	preview := previewContextAnswer(answer, 30)
	if !strings.HasPrefix(preview, "前") || !strings.HasSuffix(preview, "后") || !strings.Contains(preview, "……") {
		t.Fatalf("unexpected preview: %s", preview)
	}
	if agent.EstimateChatTokens(preview) > 35 {
		t.Fatalf("preview exceeded safe budget: %d", agent.EstimateChatTokens(preview))
	}
}

func TestExtractJSONRemovesMarkdownFence(t *testing.T) {
	content := "```json\n{\"schema_version\":1,\"topics\":[]}\n```"
	if got := extractJSON(content); got != "{\"schema_version\":1,\"topics\":[]}" {
		t.Fatalf("unexpected json: %s", got)
	}
}
