package agent

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	probeObjectSuffixes = []string{
		"客服", "后台", "控制台", "门户", "平台", "系统",
		"连接器", "数据中心", "Webhook", "API", "版", "套餐", "知识库",
	}
	probeVersionScopeTerms = []string{
		"基础版", "专业版", "旗舰版", "高级版", "新版", "旧版",
		"海外", "北美", "私有化", "专线网络",
	}
	probeTopicTerms = []string{
		"字段上限", "工单字段", "自定义工单字段", "SLA", "宽限期",
		"同步失败", "索引失败", "同步", "重试", "Webhook", "OAuth",
		"审批", "客户数据", "导出", "权限", "文件上传", "附件",
		"套餐", "额度", "会话额度", "计费", "收费", "签名校验",
		"质检", "优先级", "知识库", "私有化部署",
		"远程入职", "纸质材料", "寄回", "入职", "发票", "抬头",
		"税号", "合同金额", "合同", "个人网盘", "会话", "超量",
		"私有化", "交付", "交付周期", "字段", "扩容", "上限",
		"200个", "补卡", "远程办公", "生产权限",
	}
	probeGenericAdviceTerms = []string{
		"怎么解释", "怎么设计", "一般", "例子", "话术", "维度",
		"建议", "大致怎么走", "是什么意思", "怎么写", "解释", "设计",
	}
	probeRiskTerms = []string{
		"绕过", "免审批", "跳过", "伪造", "不留痕", "看不到",
		"静默", "清理", "私下", "共享账号", "改一下",
	}
	probeRealtimeTerms = []string{
		"今天", "现在", "最新", "公告", "官方状态", "是否改过",
		"服务事件", "官方侧故障", "服务状态", "维护窗口", "价格",
	}
	probeVagueTerms = []string{
		"这个", "那个", "这些", "那些", "该", "此", "专属",
	}
	probeAliasGroups = [][]string{
		{"方舟客服", "云舟客服", "ArkDesk"},
		{"飞书", "Lark"},
	}
	probeEnglishTokenRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_-]*`)
)

// EvaluateProbeEvidence 判断轻量探测结果是否足以进入完整RAG
func EvaluateProbeEvidence(question string, result KnowledgeProbeResult) ProbeEvidence {
	if len(result.Hits) == 0 {
		return ProbeEvidence{Level: ProbeEvidenceNone, Reasons: []string{"轻量探测无命中"}}
	}
	haystack := probeHitText(result)
	if containsAny(question, probeRiskTerms) {
		return ProbeEvidence{Level: ProbeEvidenceWeak, Reasons: []string{"问题包含高风险执行意图，应由Planner优先判断拒答"}}
	}
	if containsAny(question, probeRealtimeTerms) {
		return ProbeEvidence{Level: ProbeEvidenceWeak, Reasons: []string{"问题依赖实时公开信息，内部相似命中不足以直接完整RAG"}}
	}
	objectAnchors := extractProbeObjectAnchors(question)
	uncoveredObjects := uncoveredProbeAnchors(objectAnchors, haystack)
	if len(uncoveredObjects) > 0 {
		return ProbeEvidence{
			Level:   ProbeEvidenceWeak,
			Reasons: []string{fmt.Sprintf("命中文档未明确覆盖用户问题中的关键对象、版本或范围：%s", strings.Join(uncoveredObjects, "、"))},
		}
	}
	if len(objectAnchors) == 0 && containsAny(question, probeVagueTerms) {
		return ProbeEvidence{Level: ProbeEvidenceWeak, Reasons: []string{"问题存在指代或范围不明确，命中文档不足以直接进入完整RAG"}}
	}
	if containsAny(question, probeGenericAdviceTerms) {
		return ProbeEvidence{Level: ProbeEvidenceWeak, Reasons: []string{"问题更像通用建议或话术，不应仅因相似文档进入完整RAG"}}
	}
	topicTerms := extractProbeTopicTerms(question)
	coveredTopics := coveredProbeTermCount(topicTerms, haystack)
	if len(topicTerms) > 0 && coveredTopics == 0 {
		return ProbeEvidence{Level: ProbeEvidenceWeak, Reasons: []string{"命中文档未覆盖用户问题中的核心主题"}}
	}
	if len(objectAnchors) > 0 && coveredTopics > 0 {
		return ProbeEvidence{Level: ProbeEvidenceStrong, Reasons: []string{"命中文档覆盖关键业务对象和问题主题"}}
	}
	if len(objectAnchors) == 0 && coveredTopics > 0 {
		return ProbeEvidence{Level: ProbeEvidenceStrong, Reasons: []string{"命中文档覆盖问题主题"}}
	}
	return ProbeEvidence{Level: ProbeEvidenceWeak, Reasons: []string{"仅存在相似主题命中，证据不足以直接进入完整RAG"}}
}

func probeHitText(result KnowledgeProbeResult) string {
	var b strings.Builder
	for _, hit := range result.Hits {
		b.WriteString(" ")
		b.WriteString(hit.DocumentName)
		b.WriteString(" ")
		b.WriteString(hit.SectionPath)
		b.WriteString(" ")
		b.WriteString(hit.ContentPreview)
	}
	return strings.ToLower(b.String())
}

func extractProbeObjectAnchors(question string) []string {
	anchors := make([]string, 0, 4)
	for _, term := range probeVersionScopeTerms {
		if strings.Contains(question, term) {
			anchors = appendProbeAnchor(anchors, term)
		}
	}
	for _, suffix := range probeObjectSuffixes {
		anchors = append(anchors, phrasesBeforeSuffix(question, suffix)...)
	}
	for _, token := range probeEnglishTokenRE.FindAllString(question, -1) {
		if isProbeTopicTerm(token) {
			continue
		}
		anchors = appendProbeAnchor(anchors, token)
	}
	return anchors
}

func phrasesBeforeSuffix(text, suffix string) []string {
	matches := []string{}
	start := 0
	for {
		idx := strings.Index(text[start:], suffix)
		if idx < 0 {
			break
		}
		idx += start
		prefix := previousNameRunes(text[:idx], 4)
		if prefix != "" {
			matches = appendProbeAnchor(matches, prefix+suffix)
		}
		start = idx + len(suffix)
	}
	return matches
}

func previousNameRunes(text string, limit int) string {
	runes := []rune(text)
	out := make([]rune, 0, limit)
	for i := len(runes) - 1; i >= 0 && len(out) < limit; i-- {
		r := runes[i]
		if unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append([]rune{r}, out...)
			continue
		}
		break
	}
	return strings.TrimSpace(string(out))
}

func extractProbeTopicTerms(question string) []string {
	terms := make([]string, 0, 4)
	for _, term := range probeTopicTerms {
		if strings.Contains(strings.ToLower(question), strings.ToLower(term)) {
			terms = appendProbeAnchor(terms, term)
		}
	}
	return terms
}

func uncoveredProbeAnchors(anchors []string, haystack string) []string {
	uncovered := make([]string, 0, len(anchors))
	for _, anchor := range anchors {
		if !probeTermCovered(anchor, haystack) {
			uncovered = append(uncovered, anchor)
		}
	}
	return uncovered
}

func coveredProbeTermCount(terms []string, haystack string) int {
	count := 0
	for _, term := range terms {
		if probeTermCovered(term, haystack) {
			count++
		}
	}
	return count
}

func probeTermCovered(term, haystack string) bool {
	term = strings.TrimSpace(term)
	if term == "" {
		return true
	}
	if strings.Contains(haystack, strings.ToLower(term)) {
		return true
	}
	for _, group := range probeAliasGroups {
		if !containsFold(group, term) {
			continue
		}
		for _, alias := range group {
			if strings.Contains(haystack, strings.ToLower(alias)) {
				return true
			}
		}
	}
	return false
}

func appendProbeAnchor(anchors []string, anchor string) []string {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return anchors
	}
	for _, existing := range anchors {
		if strings.EqualFold(existing, anchor) {
			return anchors
		}
	}
	return append(anchors, anchor)
}

func isProbeTopicTerm(term string) bool {
	for _, topic := range probeTopicTerms {
		if strings.EqualFold(term, topic) {
			return true
		}
	}
	return false
}

func containsFold(terms []string, target string) bool {
	for _, term := range terms {
		if strings.EqualFold(term, target) {
			return true
		}
	}
	return false
}
