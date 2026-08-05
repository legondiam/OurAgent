package service

import (
	"context"
	"testing"

	"OurAgent/internal/config"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type directiveChatStub struct {
	einomodel.BaseChatModel
	content string
	calls   int
}

func (s *directiveChatStub) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	s.calls++
	return schema.AssistantMessage(s.content, nil), nil
}

func TestMemoryDirectiveProcessorParsesExplicitPreference(t *testing.T) {
	stub := &directiveChatStub{content: `{"operation":"remember","type":"preference","scope":"user_global","subject":"回答方式","attribute":"structure","value":"先给结论","content":"用户偏好回答时先给结论","durability":"persistent","evidence_excerpt":"记住我喜欢先给结论","residual_question":""}`}
	processor := NewMemoryDirectiveProcessor(stub, config.LongTermMemoryConfig{})
	result, err := processor.Process(context.Background(), "记住我喜欢先给结论")
	if err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 || result.Operation == nil || result.Operation.Type != "preference" || result.Operation.Scope != "user_global" {
		t.Fatalf("unexpected result: %+v, calls=%d", result, stub.calls)
	}
}

func TestMemoryDirectiveProcessorRejectsEnterpriseFact(t *testing.T) {
	stub := &directiveChatStub{content: `{"operation":"remember","type":"project_context","scope":"knowledge_base","subject":"套餐","attribute":"price","value":"100元","content":"套餐价格是100元","durability":"persistent","evidence_excerpt":"记住套餐价格是100元","residual_question":""}`}
	processor := NewMemoryDirectiveProcessor(stub, config.LongTermMemoryConfig{})
	result, err := processor.Process(context.Background(), "记住套餐价格是100元")
	if err != nil || !result.Handled || result.Operation != nil {
		t.Fatalf("expected handled rejection, got result=%+v err=%v", result, err)
	}
}

func TestMemoryDirectiveProcessorRejectsKnowledgeBasePreference(t *testing.T) {
	stub := &directiveChatStub{content: `{"operation":"remember","type":"preference","scope":"knowledge_base","subject":"回答方式","attribute":"structure","value":"先给结论","content":"用户偏好回答时先给结论","durability":"persistent","evidence_excerpt":"记住我喜欢先给结论","residual_question":""}`}
	processor := NewMemoryDirectiveProcessor(stub, config.LongTermMemoryConfig{})
	result, err := processor.Process(context.Background(), "记住我喜欢先给结论")
	if err != nil || !result.Handled || result.Operation != nil {
		t.Fatalf("expected scope rejection, got result=%+v err=%v", result, err)
	}
}

func TestMemoryDirectiveProcessorRejectsGlobalRole(t *testing.T) {
	stub := &directiveChatStub{content: `{"operation":"remember","type":"role","scope":"user_global","subject":"我的职责","attribute":"role","value":"销售经理","content":"用户负责销售团队","durability":"persistent","evidence_excerpt":"记住我是销售经理","residual_question":""}`}
	processor := NewMemoryDirectiveProcessor(stub, config.LongTermMemoryConfig{})
	result, err := processor.Process(context.Background(), "记住我是销售经理")
	if err != nil || !result.Handled || result.Operation != nil {
		t.Fatalf("expected scope rejection, got result=%+v err=%v", result, err)
	}
}

func TestValidStoredMemoryScope(t *testing.T) {
	kbID := uint64(7)
	cases := []struct {
		name       string
		memoryType string
		scope      string
		kbID       *uint64
		want       bool
	}{
		{name: "global preference", memoryType: "preference", scope: "user_global", want: true},
		{name: "knowledge base preference", memoryType: "preference", scope: "knowledge_base", kbID: &kbID},
		{name: "knowledge base role", memoryType: "role", scope: "knowledge_base", kbID: &kbID, want: true},
		{name: "global role", memoryType: "role", scope: "user_global"},
		{name: "removed project state", memoryType: "project_state", scope: "knowledge_base", kbID: &kbID},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := validStoredMemoryScope(test.memoryType, test.scope, test.kbID); got != test.want {
				t.Fatalf("validStoredMemoryScope()=%v, want %v", got, test.want)
			}
		})
	}
}
