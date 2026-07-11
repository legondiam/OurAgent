package agent

import "testing"

func TestEvaluateProbeEvidenceUnknownObjectIsWeak(t *testing.T) {
	result := KnowledgeProbeResult{
		Query:    "蓝鲸后台 字段上限",
		MaxScore: 0.62,
		Hits: []KnowledgeProbeHit{{
			DocumentName:   "product_manual.md",
			SectionPath:    "套餐版本",
			Score:          0.62,
			ContentPreview: "云舟客服专业版支持自定义工单字段，旗舰版默认字段上限为200个",
		}},
	}

	evidence := EvaluateProbeEvidence("蓝鲸后台的字段上限是多少？", result)

	if evidence.Level != ProbeEvidenceWeak {
		t.Fatalf("expected weak evidence, got %s", evidence.Level)
	}
	if len(evidence.Reasons) == 0 {
		t.Fatal("expected weak reason")
	}
}

func TestEvaluateProbeEvidenceAliasObjectAndTopicIsStrong(t *testing.T) {
	result := KnowledgeProbeResult{
		Query:    "方舟客服 基础版 工单字段",
		MaxScore: 0.81,
		Hits: []KnowledgeProbeHit{{
			DocumentName:   "product_manual.md",
			SectionPath:    "套餐版本",
			Score:          0.81,
			ContentPreview: "云舟客服基础版不支持自定义工单字段，专业版支持自定义工单字段",
		}},
	}

	evidence := EvaluateProbeEvidence("方舟客服基础版能改工单字段吗？", result)

	if evidence.Level != ProbeEvidenceStrong {
		t.Fatalf("expected strong evidence, got %s: %v", evidence.Level, evidence.Reasons)
	}
}

func TestEvaluateProbeEvidenceGenericAdviceIsWeak(t *testing.T) {
	result := KnowledgeProbeResult{
		Query:    "星河客服 SLA违约 解释话术",
		MaxScore: 0.72,
		Hits: []KnowledgeProbeHit{{
			DocumentName:   "product_manual.md",
			SectionPath:    "SLA与私有化",
			Score:          0.72,
			ContentPreview: "云舟客服旗舰版P0故障10分钟响应，专业版P0故障15分钟响应",
		}},
	}

	evidence := EvaluateProbeEvidence("星河客服里SLA违约这个说法怎么跟客户解释？", result)

	if evidence.Level != ProbeEvidenceWeak {
		t.Fatalf("expected weak evidence, got %s", evidence.Level)
	}
	if evidence.Reasons[0] != "命中文档未明确覆盖用户问题中的关键对象、版本或范围：星河客服" {
		t.Fatalf("expected object mismatch reason, got %v", evidence.Reasons)
	}
}

func TestEvaluateProbeEvidenceNoHitsIsNone(t *testing.T) {
	evidence := EvaluateProbeEvidence("高级版这个限制是多少？", KnowledgeProbeResult{Query: "高级版 限制"})

	if evidence.Level != ProbeEvidenceNone {
		t.Fatalf("expected none evidence, got %s", evidence.Level)
	}
}

func TestEvaluateProbeEvidenceKnownExternalObjectIsStrong(t *testing.T) {
	result := KnowledgeProbeResult{
		Query:    "Notion 同步失败 重试",
		MaxScore: 0.79,
		Hits: []KnowledgeProbeHit{{
			DocumentName:   "integrations.md",
			SectionPath:    "Notion同步",
			Score:          0.79,
			ContentPreview: "Notion同步失败后会自动重试3次，延迟分别为1分钟、5分钟和15分钟",
		}},
	}

	evidence := EvaluateProbeEvidence("Notion同步失败会一直重试吗？", result)

	if evidence.Level != ProbeEvidenceStrong {
		t.Fatalf("expected strong evidence, got %s: %v", evidence.Level, evidence.Reasons)
	}
}

func TestEvaluateProbeEvidenceUnlistedObjectSuffixIsWeak(t *testing.T) {
	result := KnowledgeProbeResult{
		Query:    "机器人套餐 超量收费",
		MaxScore: 0.7,
		Hits: []KnowledgeProbeHit{{
			DocumentName:   "product_manual.md",
			SectionPath:    "商业化套餐",
			Score:          0.7,
			ContentPreview: "基础版包含每月1万次会话额度，专业版包含每月20万次会话额度",
		}},
	}

	evidence := EvaluateProbeEvidence("那个机器人套餐超量后怎么收费？", result)

	if evidence.Level != ProbeEvidenceWeak {
		t.Fatalf("expected weak evidence, got %s", evidence.Level)
	}
}

func TestEvaluateProbeEvidenceGrayEnterpriseTopicIsStrong(t *testing.T) {
	result := KnowledgeProbeResult{
		Query:    "基础版 会话超量 接入",
		MaxScore: 0.76,
		Hits: []KnowledgeProbeHit{{
			DocumentName:   "product_manual.md",
			SectionPath:    "商业化套餐",
			Score:          0.76,
			ContentPreview: "基础版包含每月1万次会话额度，超过会话额度后会限制新会话接入",
		}},
	}

	evidence := EvaluateProbeEvidence("基础版会话超量后还能继续接入吗？", result)

	if evidence.Level != ProbeEvidenceStrong {
		t.Fatalf("expected strong evidence, got %s: %v", evidence.Level, evidence.Reasons)
	}
}

func TestEvaluateProbeEvidenceSecurityPermissionQuestionIsStrong(t *testing.T) {
	result := KnowledgeProbeResult{
		Query:    "合同金额 个人网盘 安全规范",
		MaxScore: 0.74,
		Hits: []KnowledgeProbeHit{{
			DocumentName:   "security_policy.md",
			SectionPath:    "敏感数据",
			Score:          0.74,
			ContentPreview: "合同金额属于敏感数据，不得通过个人网盘传输，客户数据导出需要审批",
		}},
	}

	evidence := EvaluateProbeEvidence("我能把合同金额发到个人网盘吗？", result)

	if evidence.Level != ProbeEvidenceStrong {
		t.Fatalf("expected strong evidence, got %s: %v", evidence.Level, evidence.Reasons)
	}
}
