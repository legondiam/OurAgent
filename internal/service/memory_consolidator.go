package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const memoryConsolidationPrompt = `你是企业助手的长期记忆提取器。只从用户原话提取跨会话仍有价值的稳定背景。
输出JSON：{"candidates":[{"type":"role|business_object|project_context|terminology|instruction","subject":"","attribute":"","value":"","content":"","durability":"temporary|persistent","confidence":0.0,"importance":0.0,"source_chat_log_id":1,"evidence_excerpt":"用户原文中的连续原句"}]}
规则：
1. 最多输出指定数量，没价值时返回空数组
2. 不提取助手回答或模型推断
3. 不提取产品能力、套餐、价格、制度、权限、API参数、版本能力或其他企业公共事实
4. 不提取密码、Token、密钥、身份号、银行卡、客户原始数据或绕过审批权限审计的指令
5. evidence_excerpt必须逐字出现在对应用户消息中
6. 推测性表达可以形成candidate，但不得声称已确认
7. 普通提取的scope固定为knowledge_base，不提取preference`

type MemoryConsolidator struct {
	repo *repository.LongTermMemoryRepository
	logs *repository.ChatLogRepository
	chat einomodel.BaseChatModel
	cfg  config.LongTermMemoryConfig
}

type memoryCandidateOutput struct {
	Type            string  `json:"type"`
	Subject         string  `json:"subject"`
	Attribute       string  `json:"attribute"`
	Value           string  `json:"value"`
	Content         string  `json:"content"`
	Durability      string  `json:"durability"`
	Confidence      float64 `json:"confidence"`
	Importance      float64 `json:"importance"`
	SourceChatLogID uint64  `json:"source_chat_log_id"`
	EvidenceExcerpt string  `json:"evidence_excerpt"`
}

// NewMemoryConsolidator 创建长期记忆批量归并器
func NewMemoryConsolidator(repo *repository.LongTermMemoryRepository, logs *repository.ChatLogRepository, chat einomodel.BaseChatModel, cfg config.LongTermMemoryConfig) *MemoryConsolidator {
	return &MemoryConsolidator{repo: repo, logs: logs, chat: chat, cfg: cfg}
}

// Consolidate 从固定Signal快照批量提取和合并候选记忆
func (c *MemoryConsolidator) Consolidate(ctx context.Context, taskID string, signalIDs []uint64) error {
	//按任务读取用户归属，避免调用方传递可伪造的用户ID
	job, err := c.repo.FindJob(taskID)
	if err != nil {
		return err
	}
	signals, err := c.repo.SignalsByIDs(job.UserID, signalIDs)
	if err != nil {
		return err
	}
	if len(signals) == 0 {
		return nil
	}
	if len(signals) != len(signalIDs) {
		return fmt.Errorf("长期记忆Signal快照不完整")
	}
	ids := make([]uint64, 0, len(signals))
	for _, signal := range signals {
		ids = append(ids, signal.ChatLogID)
	}
	logs, err := c.logs.FindManyOwnedByConversation(signals[0].UserID, signals[0].KnowledgeBaseID, signals[0].ConversationID, ids)
	if err != nil {
		return err
	}
	byID := make(map[uint64]model.ChatLog, len(logs))
	var input strings.Builder
	maxCandidates := c.cfg.MaxCandidatesPerBatch
	if maxCandidates <= 0 {
		maxCandidates = 5
	}
	input.WriteString(fmt.Sprintf("最多输出%d条候选。用户消息：\n", maxCandidates))
	for _, log := range logs {
		byID[log.ID] = log
		input.WriteString(fmt.Sprintf("chat_log_id=%d\n用户：%s\n", log.ID, log.Question))
	}
	timeout := c.cfg.ConsolidationTimeoutSeconds
	if timeout <= 0 {
		timeout = 120
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	response, err := c.chat.Generate(callCtx, []*schema.Message{schema.SystemMessage(memoryConsolidationPrompt), schema.UserMessage(input.String())})
	if err != nil {
		return err
	}
	var output struct {
		Candidates []memoryCandidateOutput `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(extractJSON(response.Content)), &output); err != nil {
		return fmt.Errorf("解析长期记忆候选失败: %w", err)
	}
	if len(output.Candidates) > maxCandidates {
		output.Candidates = output.Candidates[:maxCandidates]
	}
	for _, candidate := range output.Candidates {
		log, ok := byID[candidate.SourceChatLogID]
		if !ok || candidate.Type == "preference" || !strings.Contains(log.Question, candidate.EvidenceExcerpt) || !allowedMemoryCandidate(candidate) {
			continue
		}
		kbID := signals[0].KnowledgeBaseID
		identity := memoryIdentityHash(signals[0].UserID, model.MemoryScopeKnowledgeBase, kbID, candidate.Type, candidate.Subject, candidate.Attribute)
		now := time.Now()
		expires := now.Add(time.Duration(memoryTTLHours(c.cfg, candidate.Type, true)) * time.Hour)
		memory := model.LongTermMemory{UserID: signals[0].UserID, KnowledgeBaseID: &kbID, Scope: model.MemoryScopeKnowledgeBase, Type: candidate.Type, MemoryKey: strings.ToLower(strings.TrimSpace(candidate.Subject + "." + candidate.Attribute)), IdentityHash: identity, Subject: strings.TrimSpace(candidate.Subject), Attribute: strings.TrimSpace(candidate.Attribute), Value: strings.TrimSpace(candidate.Value), Content: strings.TrimSpace(candidate.Content), Status: model.MemoryStatusCandidate, Durability: candidate.Durability, Confidence: candidate.Confidence, Importance: candidate.Importance, Version: 1, EmbeddingStatus: model.MemoryEmbeddingPending, FirstObservedAt: now, ExpiresAt: &expires, CreatedAt: now, UpdatedAt: now}
		evidence := model.LongTermMemoryEvidence{UserID: memory.UserID, ConversationID: log.ConversationID, ChatLogID: log.ID, EvidenceHash: repository.HashMemoryText(candidate.EvidenceExcerpt), EvidenceKind: "user_statement", Explicit: false, CreatedAt: now}
		if err := c.repo.MergeCandidate(&memory, evidence, c.cfg.AutoPromoteMinConversations); err != nil {
			return err
		}
	}
	return nil
}

func allowedMemoryCandidate(candidate memoryCandidateOutput) bool {
	allowed := map[string]bool{"role": true, "preference": true, "business_object": true, "project_context": true, "terminology": true, "instruction": true}
	if !allowed[candidate.Type] || strings.TrimSpace(candidate.Subject) == "" || strings.TrimSpace(candidate.Content) == "" || strings.TrimSpace(candidate.EvidenceExcerpt) == "" {
		return false
	}
	text := candidate.Content + candidate.Value
	for _, pattern := range enterpriseFactPatterns {
		if pattern.MatchString(text) {
			return false
		}
	}
	return !containsSensitiveMemory(text)
}

func containsSensitiveMemory(text string) bool {
	lower := strings.ToLower(text)
	for _, word := range []string{"password", "api key", "apikey", "access_token", "private key", "密码", "银行卡", "身份证", "私钥", "绕过审批", "绕过权限", "隐藏审计"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func containsEnterpriseMemoryFact(text string) bool {
	for _, pattern := range enterpriseFactPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func memoryIdentityHash(userID uint64, scope string, kbID uint64, memoryType, subject, attribute string) string {
	raw := fmt.Sprintf("%d\x00%s\x00%d\x00%s\x00%s\x00%s", userID, scope, kbID, strings.ToLower(strings.TrimSpace(memoryType)), strings.ToLower(strings.TrimSpace(subject)), strings.ToLower(strings.TrimSpace(attribute)))
	return fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
}

func memoryTTLHours(cfg config.LongTermMemoryConfig, memoryType string, candidate bool) int {
	if candidate && cfg.CandidateExpireHours > 0 {
		return cfg.CandidateExpireHours
	}
	values := map[string]int{"preference": cfg.PreferenceTTLHours, "role": cfg.RoleTTLHours, "terminology": cfg.TerminologyTTLHours, "business_object": cfg.BusinessObjectTTLHours, "project_context": cfg.ProjectContextTTLHours}
	if value := values[memoryType]; value > 0 {
		return value
	}
	return 720
}
