package service

import (
	"testing"

	"OurAgent/internal/agent"
)

func TestMemoryWorthinessGate(t *testing.T) {
	gate := MemoryWorthinessGate{}
	cases := []struct {
		text string
		want MemoryWorthiness
	}{
		{"报销流程怎么走？", MemoryWorthinessNone},
		{"我负责华东区的售前工作", MemoryWorthinessCandidate},
		{"我们说的老同步器指旧版数据同步服务", MemoryWorthinessCandidate},
		{"记住我喜欢先给结论", MemoryWorthinessExplicit},
		{"套餐价格是每月100元", MemoryWorthinessNone},
	}
	for _, tc := range cases {
		if got := gate.Evaluate(tc.text); got != tc.want {
			t.Fatalf("Evaluate(%q)=%s, want %s", tc.text, got, tc.want)
		}
	}
}

func TestMemoryRecallGate(t *testing.T) {
	gate := MemoryRecallGate{}
	if got := gate.Evaluate("继续上次那个项目", false); !got.Semantic || got.Reason != "cross_conversation_reference" {
		t.Fatalf("unexpected decision: %+v", got)
	}
	if got := gate.Evaluate("报销流程怎么走", false); got.Semantic {
		t.Fatalf("ordinary query should not trigger semantic recall: %+v", got)
	}
}

func TestTrimMemoryItemsHonorsLimits(t *testing.T) {
	items := []agent.LongTermMemoryItem{{MemoryID: 1, Content: "短背景"}, {MemoryID: 2, Content: "另一个背景"}, {MemoryID: 3, Content: "第三个背景"}}
	selected := trimMemoryItems(items, 2, 100)
	if len(selected) != 2 || selected[0].MemoryID != 1 || selected[1].MemoryID != 2 {
		t.Fatalf("unexpected selected items: %+v", selected)
	}
}
