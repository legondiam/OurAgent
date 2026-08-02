package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestEstimateChatTokensUsesConservativeCJKCount(t *testing.T) {
	if got := EstimateChatTokens("中文测试"); got != 4 {
		t.Fatalf("expected 4 tokens, got %d", got)
	}
	if got := EstimateChatTokens("abcdefgh"); got != 2 {
		t.Fatalf("expected 2 tokens, got %d", got)
	}
}

func TestRenderConversationSummaryIncludesTopicsAndPendingQuestions(t *testing.T) {
	summary := ConversationSummary{
		SchemaVersion: 1,
		ActiveTopic:   "topic_2",
		Topics: []ConversationTopic{
			{Key: "topic_1", Title: "报销流程", Overview: "讨论差旅审批", DiscussionResults: []DiscussionResult{{Content: "需主管审批", SourceChatLogIDs: []uint64{18}}}},
			{Key: "topic_2", Title: "配额权限", UserGoal: "确认管理员权限", PendingQuestions: []string{"是否使用自定义角色"}},
		},
	}
	rendered := RenderConversationSummary(summary)
	for _, expected := range []string{"报销流程", "配额权限（当前）", "确认管理员权限", "是否使用自定义角色", "来源chat_log_id=18"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected rendered summary to contain %q: %s", expected, rendered)
		}
	}
}

func TestNormalizeConversationAnswerRequiresSourceLogs(t *testing.T) {
	input := PlannerInput{
		Stage: PlannerStageContextResolved,
		Tools: []ToolSpec{{Name: string(ActionConversationAnswer)}},
	}
	decision := NormalizeDecision(Decision{Action: ActionConversationAnswer, Reason: "缩写上一轮"}, input, SearchPlan{})
	if decision.Action != ActionClarify {
		t.Fatalf("expected clarify without source logs, got %s", decision.Action)
	}

	decision = NormalizeDecision(Decision{Action: ActionConversationAnswer, Reason: "缩写上一轮", SourceChatLogIDs: []uint64{8}}, input, SearchPlan{})
	if decision.Action != ActionConversationAnswer {
		t.Fatalf("expected conversation answer, got %s", decision.Action)
	}
}

func TestParseConversationAnswerToolCall(t *testing.T) {
	input := PlannerInput{
		Stage: PlannerStageContextResolved,
		Tools: []ToolSpec{{Name: string(ActionConversationAnswer)}},
	}
	calls := []schema.ToolCall{{Function: schema.FunctionCall{
		Name:      string(ActionConversationAnswer),
		Arguments: `{"reason":"缩写上一轮","source_chat_log_ids":[8,9]}`,
	}}}
	decision, err := ParseToolCallDecision(calls, input)
	if err != nil {
		t.Fatalf("parse tool call: %v", err)
	}
	if decision.Action != ActionConversationAnswer || len(decision.SourceChatLogIDs) != 2 {
		t.Fatalf("unexpected decision: %+v", decision)
	}

	calls[0].Function.Arguments = `{"reason":"缩写上一轮","source_chat_log_ids":[0]}`
	if _, err := ParseToolCallDecision(calls, input); err == nil {
		t.Fatal("expected zero source id to be rejected")
	}
}
