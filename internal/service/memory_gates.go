package service

import (
	"regexp"
	"strings"
)

type MemoryWorthiness string

const (
	MemoryWorthinessNone      MemoryWorthiness = "none"
	MemoryWorthinessCandidate MemoryWorthiness = "candidate"
	MemoryWorthinessExplicit  MemoryWorthiness = "explicit"
)

type MemoryWorthinessGate struct{}

var (
	explicitMemoryPattern = regexp.MustCompile(`(?i)(记住|记一下|帮我记|请记得|以后称|纠正.{0,12}(记忆|之前)|忘掉|忘记|不要再记)`)
	stableContextPatterns = []*regexp.Regexp{
		regexp.MustCompile(`我(是|负责|主要负责|正在负责|在跟进|正在跟进|长期负责)`),
		regexp.MustCompile(`(这个|该|我们)(项目|客户|环境|业务对象).{0,30}(是|采用|处于|叫|指)`),
		regexp.MustCompile(`(我说的|我们说的|以后说|简称|别名).{0,20}(是|指|代表|叫)`),
		regexp.MustCompile(`(?i)(my role|I am responsible for|we call|means)`),
	}
	enterpriseFactPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(价格|套餐|权限规则|制度条款|API参数|版本能力|产品能力|收费|上限).{0,12}(是|为|支持|允许|限制)`),
	}
)

// Evaluate 判断用户消息是否值得进入长期记忆链路
func (MemoryWorthinessGate) Evaluate(text string) MemoryWorthiness {
	text = strings.TrimSpace(text)
	if text == "" {
		return MemoryWorthinessNone
	}
	if explicitMemoryPattern.MatchString(text) {
		return MemoryWorthinessExplicit
	}
	for _, pattern := range enterpriseFactPatterns {
		if pattern.MatchString(text) {
			return MemoryWorthinessNone
		}
	}
	for _, pattern := range stableContextPatterns {
		if pattern.MatchString(text) {
			return MemoryWorthinessCandidate
		}
	}
	return MemoryWorthinessNone
}

type MemoryRecallDecision struct {
	Semantic bool
	Reason   string
}

type MemoryRecallGate struct{}

// Evaluate 判断当前问题是否需要语义召回长期记忆
func (MemoryRecallGate) Evaluate(question string, shortContextInsufficient bool) MemoryRecallDecision {
	question = strings.TrimSpace(question)
	patterns := []struct {
		reason string
		words  []string
	}{
		{"cross_conversation_reference", []string{"上次", "之前", "以前", "继续", "还记得"}},
		{"business_reference", []string{"这个项目", "那个项目", "那个客户", "客户环境", "这个环境"}},
		{"user_background", []string{"我负责的", "我正在跟进的", "我的项目", "我的客户"}},
	}
	for _, item := range patterns {
		for _, word := range item.words {
			if strings.Contains(question, word) {
				return MemoryRecallDecision{Semantic: true, Reason: item.reason}
			}
		}
	}
	if shortContextInsufficient && strings.ContainsAny(question, "这那它其") {
		return MemoryRecallDecision{Semantic: true, Reason: "unresolved_reference"}
	}
	return MemoryRecallDecision{}
}

type MemoryDirectiveMatcher struct{}

// MayContainDirective 判断消息是否可能包含显式记忆指令
func (MemoryDirectiveMatcher) MayContainDirective(text string) bool {
	return explicitMemoryPattern.MatchString(strings.TrimSpace(text))
}
