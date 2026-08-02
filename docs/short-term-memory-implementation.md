# Agent短期记忆实现说明

## 1. 背景

当前Agent通过`context_lookup`工具按需读取最近5轮问答，并对单条回答和历史总字符数进行裁剪。该实现能够处理简单追问，但存在以下问题：

- Planner在第一次决策时看不到会话历史，可能无法判断当前问题依赖上下文
- 只读取最近5轮，无法覆盖较长会话
- 单条回答最多保留300字符，可能丢失版本、条件和结论
- 按字符而不是Token控制上下文预算
- 缺少会话摘要，历史超过窗口后只能直接丢弃
- `chat_logs`隐式承担会话存在性判断，没有独立的会话生命周期

本次改造将短期记忆从模型可选工具调整为Agent运行前自动执行的内部组件，并在会话历史超过阈值后异步压缩旧历史。

## 2. 目标

- 短会话向Planner提供完整原始历史
- 长会话提供结构化摘要和最近原始历史
- 每轮Agent执行前自动装配短期上下文
- 摘要调用始终异步，不增加主问答接口的LLM调用次数
- 支持同一会话内切换和恢复多个话题
- 支持基于既有回答进行复述、缩写、翻译和格式转换
- 保证用户、知识库和会话三级隔离
- 同一会话串行执行，不同会话并行执行
- 摘要链路异常时不影响核心问答
- 通过配置开关支持回退到旧`context_lookup`链路

## 3. 非目标

- 不实现跨会话长期记忆
- 不引入独立的`conversation_messages`表
- 不删除被摘要覆盖的原始`chat_logs`
- 不把会话摘要作为企业事实的权威来源
- 不支持同一会话的消息分支和并行分叉
- 不迁移现有`chat_logs`中的旧会话

## 4. 核心决策

### 4.1 存储职责

`chat_logs`继续作为已完成问答轮次的原始记录，新增`conversations`保存：

- 会话归属和生命周期
- 结构化摘要
- 摘要覆盖游标
- 未摘要历史Token数
- 摘要任务状态
- 会话请求租约

本阶段不新增`conversation_messages`。当前Agent的一轮完整问答已经能够通过`Question`、`Answer`、`AnswerMode`、`AgentTrace`和`RetrievalTrace`表达，拆分消息表会引入不必要的双写和迁移成本。

### 4.2 读取时机

启用新链路后，只要请求携带有效的`conversation_id`，服务端就在Planner执行前自动调用`ConversationContextAssembler`。短期上下文不再由模型决定是否读取。

### 4.3 压缩时机

每轮成功回答后保存原始问答并更新未摘要Token数。超过软阈值时发布RabbitMQ异步摘要任务，不在主请求内同步调用摘要模型。

### 4.4 降级原则

RabbitMQ关闭、发布失败、摘要失败或任务未及时完成时，Assembler按硬Token预算临时选择最新的完整问答。该操作只影响本轮Prompt，不删除数据库数据。

### 4.5 企业事实边界

短期记忆可以复述或变换已经返回给用户的答案，但不能仅根据摘要扩展新的企业事实。涉及版本变化、有效性、适用范围、例外或新判断时必须重新查询知识库。

## 5. 总体架构

```text
Agent请求
  |
  +-- 新会话
  |     |
  |     +-- 内存生成conversation_id
  |     +-- Pre-RAG Planner
  |
  +-- 已有会话
        |
        +-- 校验会话归属和有效期
        +-- 抢占会话请求租约
        +-- ConversationContextAssembler
        |     |
        |     +-- 读取SummaryJSON
        |     +-- 读取摘要游标之后的chat_logs
        |     +-- 按Token预算组装上下文
        |
        +-- Context-Resolved Planner

Planner
  |
  +-- conversation_answer
  +-- direct_answer
  +-- knowledge_probe
  +-- knowledge_search
  +-- clarify
  +-- web_search
  +-- reject

最终回答
  |
  +-- 事务保存ChatLog和Conversation状态
  +-- 释放会话请求租约
  +-- 提交后检查摘要阈值
        |
        +-- 未超过：结束
        +-- 超过：发布异步压缩任务
```

## 6. 数据模型

### 6.1 Conversation

```go
type Conversation struct {
	ID                    string         `gorm:"size:64;primaryKey" json:"id"`
	UserID                uint64         `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID       uint64         `gorm:"index;not null" json:"knowledge_base_id"`
	Status                string         `gorm:"size:32;index;not null" json:"status"`
	SummaryJSON           datatypes.JSON `gorm:"type:json" json:"summary_json"`
	SummarySchemaVersion  int            `gorm:"not null;default:1" json:"summary_schema_version"`
	SummarizedThroughID   uint64         `gorm:"not null;default:0" json:"summarized_through_id"`
	UnsummarizedTokens    int            `gorm:"not null;default:0" json:"unsummarized_tokens"`
	SummaryVersion        uint64         `gorm:"not null;default:0" json:"summary_version"`
	SummaryStatus         string         `gorm:"size:32;index;not null" json:"summary_status"`
	SummaryTaskID         string         `gorm:"size:64;index" json:"summary_task_id"`
	SummaryAttempt        int            `gorm:"not null;default:0" json:"summary_attempt"`
	SummaryLeaseUntil     *time.Time     `gorm:"index" json:"summary_lease_until"`
	LastSummaryError      string         `gorm:"type:text" json:"last_summary_error"`
	ProcessingToken       string         `gorm:"size:64;index" json:"processing_token"`
	ProcessingLeaseUntil  *time.Time     `gorm:"index" json:"processing_lease_until"`
	LastMessageAt         time.Time      `gorm:"index;not null" json:"last_message_at"`
	ExpiresAt             time.Time      `gorm:"index;not null" json:"expires_at"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}
```

状态常量：

```text
ConversationStatusActive  = active
ConversationStatusExpired = expired

SummaryStatusIdle       = idle
SummaryStatusQueued     = queued
SummaryStatusProcessing = processing
SummaryStatusFailed     = failed
```

建议增加复合索引：

```text
(user_id, knowledge_base_id, status, expires_at)
```

### 6.2 ChatLog扩展

```go
ConversationTokens int `gorm:"not null;default:0" json:"conversation_tokens"`
```

建议增加复合索引：

```text
(user_id, knowledge_base_id, conversation_id, id)
```

`ConversationTokens`只计算用户问题和最终回答，不使用现有`PromptTokens`。`PromptTokens`可能包含RAG文档上下文，不能代表会话历史大小。

## 7. 摘要结构

摘要保存为带版本号的结构化JSON，同时保留自然语言概述：

```json
{
  "schema_version": 1,
  "active_topic": "topic_2",
  "topics": [
    {
      "key": "topic_1",
      "title": "报销审批流程",
      "status": "paused",
      "overview": "已确认研发部差旅报销适用2026版制度",
      "user_goal": "确认差旅报销审批链路",
      "entities": [
        {
          "type": "department",
          "value": "研发部"
        }
      ],
      "confirmed_constraints": [
        "仅讨论2026版制度"
      ],
      "discussion_results": [
        {
          "content": "已讨论部门审批和财务审批的顺序",
          "source_chat_log_ids": [101, 105]
        }
      ],
      "pending_questions": [],
      "user_corrections": []
    },
    {
      "key": "topic_2",
      "title": "WeDrive配额权限",
      "status": "active",
      "overview": "正在确认私有化版本的部门管理员权限",
      "user_goal": "确认部门管理员是否能修改成员空间配额",
      "entities": [
        {
          "type": "deployment",
          "value": "private"
        }
      ],
      "confirmed_constraints": [],
      "discussion_results": [],
      "pending_questions": [
        "是否使用自定义角色"
      ],
      "user_corrections": []
    }
  ]
}
```

摘要规则：

- 当前问题优先于历史主题
- 新话题创建新的Topic，原活跃Topic改为`paused`或`resolved`
- 用户回到旧话题时更新`active_topic`
- 用户纠正信息后，新值成为有效实体，旧值不再参与Query补全
- `discussion_results`只是会话进度，不是企业知识权威来源
- 进行中或存在待确认项的话题优先保留详细信息
- 已完成的旧话题在摘要接近目标长度时压缩成简短索引
- 所有`source_chat_log_ids`必须属于当前用户、知识库和会话

数据库保存JSON，Assembler将其渲染为稳定的自然语言段落后再传给Planner。

## 8. Token估算和默认配置

### 8.1 默认配置

```yaml
agent_memory:
  short_term_enabled: true
  summary_trigger_tokens: 4000
  context_hard_limit_tokens: 6000
  keep_recent_tokens: 2000
  summary_target_tokens: 1000
  summary_timeout_seconds: 120
  compaction_lease_seconds: 180
  conversation_processing_lease_seconds: 180
  conversation_ttl_hours: 168
```

参数含义：

- `summary_trigger_tokens`：未摘要原始历史达到该值后异步发布摘要任务
- `context_hard_limit_tokens`：单次向Planner注入的短期上下文上限
- `keep_recent_tokens`：压缩时必须尽量保留的最近原始历史
- `summary_target_tokens`：摘要模型输出目标上限
- `conversation_ttl_hours`：最后一次成功问答后允许继续会话的时间，默认7天

### 8.2 会话Token估算

现有`document.EstimateTokens`按两个Unicode字符估算一个Token，可能低估中文会话。短期记忆增加更保守的估算函数：

```go
func EstimateChatTokens(text string) int {
	tokens := 0
	asciiRunes := 0
	for _, r := range text {
		if r <= 127 {
			asciiRunes++
			continue
		}
		tokens++
	}
	tokens += (asciiRunes + 3) / 4
	return tokens
}
```

每轮Token数：

```text
EstimateChatTokens(question)
+ EstimateChatTokens(answer)
+ 固定消息封装开销
```

实现时应使用统一常量表示固定消息开销，避免Repository和Assembler计算不一致。

## 9. ConversationContextAssembler

`ConversationContextAssembler`是服务端内部组件，不是Agent Tool，也不调用LLM。

```go
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

type ConversationContext struct {
	ConversationID      string
	Summary             string
	Messages            []HistoryMessage
	SummarizedThroughID uint64
	EstimatedTokens     int
	Degraded            bool
	DegradedReason      string
}
```

### 9.1 组装算法

1. 读取`Conversation`
2. 校验`user_id`、`knowledge_base_id`和`conversation_id`
3. 检查`status`和`expires_at`
4. 渲染`SummaryJSON`
5. 查询`chat_logs.id > summarized_through_id`的日志并按`id asc`排序
6. 计算摘要和原始消息总Token
7. 未超过硬预算时返回全部内容
8. 超过硬预算时从最新日志向前选择完整问答
9. 选择结束后反转为时间正序
10. 设置`Degraded=true`并记录裁剪原因

硬预算裁剪只影响本轮Prompt，不修改`chat_logs`、`SummaryJSON`或摘要游标。

单条历史回答超过剩余预算时，Planner上下文只保留该轮问题和回答首尾预览。`conversation_answer`需要使用该轮内容时，再根据日志ID读取完整原文。

### 9.2 Planner输入

新会话继续使用`PlannerStagePreRAG`且`Context=nil`。

已有会话直接组装上下文并进入`PlannerStageContextResolved`，不再执行第一次无上下文Planner，也不向模型暴露`context_lookup`。

删除以下新链路行为：

- Planner选择`context_lookup`
- `planWithConversationContext`二次规划
- 上一轮为`clarify`时的特殊强制分支

澄清接续由每轮自动加载的上下文和摘要中的`pending_questions`统一处理。

## 10. Planner改造

### 10.1 删除新链路中的context_lookup

启用短期记忆新链路后：

- `availableTools`不再返回`context_lookup`
- Pre-RAG和Context-Resolved提示词不再要求模型先读取上下文
- Context-Resolved Planner直接输出最终动作
- `ActionContextLookup`只在灰度期间的旧链路保留

### 10.2 上下文使用规则

Planner提示词必须明确：

- 当前用户问题优先于历史
- 只使用与当前问题相关的话题
- 允许用户在同一会话切换新话题
- 不得把其他话题的实体加入`search_plan.query`
- 涉及新的企业事实判断时重新查询知识库
- 只有复述、缩写、翻译、格式转换或对比既有回答时才能选择`conversation_answer`

Trace记录：

```json
{
  "context_used": true,
  "matched_topic": "topic_1",
  "context_reason": "用户重新询问之前的报销流程"
}
```

Planner不需要也不允许返回摘要正文。

## 11. conversation_answer

新增动作：

```go
const ActionConversationAnswer Action = "conversation_answer"
```

Decision增加：

```go
SourceChatLogIDs []uint64 `json:"source_chat_log_ids,omitempty"`
```

Planner选择该动作时必须返回至少一个来源日志ID。服务端校验：

- 日志属于当前用户、知识库和会话
- 日志ID存在于当前上下文或摘要引用范围
- 来源数量不超过服务端上限
- 来源日志包含可用回答

适用场景：

- 缩写或扩写上一轮回答
- 翻译上一轮回答
- 提取上一轮某一点
- 将既有回答转换为表格、邮件或其他格式
- 比较会话中已经给出的多个方案

不适用场景：

- 判断政策是否仍然有效
- 判断新版本、新产品或新部门是否适用
- 补充之前没有回答过的例外和范围
- 基于摘要推导新的企业事实

执行流程：

1. 批量读取并校验来源`ChatLog`
2. 构建只允许内容变换、不允许新增企业事实的Prompt
3. 复用当前ChatModel生成回答
4. 合并并去重来源日志的`RetrievedChunks`
5. 设置`AnswerMode=conversation_answer`
6. 在AgentTrace记录来源日志ID和是否复用来源
7. 保存为新的完整问答轮次

如果来源日志缺失或校验失败，不允许仅根据摘要生成企业结论。根据当前问题回退到`knowledge_search`或`clarify`。

## 12. 会话生命周期

### 12.1 新会话

请求开始时只在内存生成`conversation_id`。Agent成功产生最终回答后，在同一数据库事务内：

1. 插入`conversations`
2. 插入第一条`chat_logs`
3. 初始化`UnsummarizedTokens`
4. 设置`LastMessageAt`
5. 设置`ExpiresAt=now+168h`

事务提交成功后才能向客户端返回`conversation_id`。

`clarify`、`reject`、`direct_answer`和`conversation_answer`都属于成功完成的Agent回答，应创建并延续会话。

### 12.2 已有会话

成功回答后在同一事务内：

1. 插入`chat_logs`
2. 原子增加`UnsummarizedTokens`
3. 更新`LastMessageAt`
4. 延长`ExpiresAt`
5. 条件释放当前请求的会话租约

事务提交后再检查是否需要发布摘要任务。

### 12.3 过期会话

会话在最后一次成功问答7天后过期。`resolveConversation`惰性判断，不要求定时任务提前更新状态。

客户端继续使用过期ID时返回：

```http
410 Gone
```

```json
{
  "code": "conversation_expired",
  "message": "会话已过期，请开始新会话"
}
```

过期不删除原始日志和摘要，但不再允许追加问答。摘要消费者遇到过期会话直接跳过。

## 13. 单会话并发控制

不同`conversation_id`可以并行处理，同一个`conversation_id`同一时间只允许一个活跃请求。

请求开始时使用条件更新抢占短租约：

```sql
UPDATE conversations
SET processing_token = ?,
    processing_lease_until = ?
WHERE id = ?
  AND user_id = ?
  AND knowledge_base_id = ?
  AND status = 'active'
  AND expires_at > NOW()
  AND (
      processing_token = ''
      OR processing_lease_until IS NULL
      OR processing_lease_until < NOW()
  )
```

抢占失败返回：

```http
409 Conflict
```

```json
{
  "code": "conversation_busy",
  "message": "当前会话正在处理上一条消息，请稍后重试"
}
```

请求成功、失败或取消时按`processing_token`条件释放租约。租约默认180秒，长于当前Agent接口120秒超时，进程崩溃后可自动恢复。

不支持同一会话并行分支。如果未来需要并行分支，应单独设计`parent_message_id`、`branch_id`和消息表。

## 14. 异步摘要任务

### 14.1 发布条件

事务提交后满足以下条件时尝试发布：

- 短期记忆新链路已启用
- RabbitMQ已启用且Producer可用
- 会话仍然有效
- `unsummarized_tokens >= summary_trigger_tokens`
- 当前没有`queued`或有效租约内的`processing`任务

通过条件更新生成唯一`summary_task_id`并将状态改为`queued`，再发布RabbitMQ消息。发布失败时将同一任务标记为`failed`并记录错误，主问答仍然成功返回。

任务消息至少包含：

```go
type ConversationCompactMessage struct {
	TaskID              string `json:"task_id"`
	ConversationID      string `json:"conversation_id"`
	UserID              uint64 `json:"user_id"`
	KnowledgeBaseID     uint64 `json:"knowledge_base_id"`
	SnapshotLastLogID   uint64 `json:"snapshot_last_log_id"`
	BaseSummaryVersion  uint64 `json:"base_summary_version"`
	Attempt             int    `json:"attempt"`
}
```

### 14.2 消费流程

1. 按`task_id`和租约条件抢占任务
2. 校验会话未过期
3. 读取当前`SummaryJSON`
4. 读取`(summarized_through_id, snapshot_last_log_id]`范围日志
5. 从最新日志向前保留约`keep_recent_tokens`
6. 选择更旧的完整日志作为本次压缩范围
7. 使用当前ChatModel生成完整的新`SummaryJSON`
8. 校验JSON Schema、字段长度和来源日志归属
9. 条件更新摘要、游标和SummaryVersion
10. 从`UnsummarizedTokens`原子扣减本次被压缩的Token数
11. 清理任务状态和租约
12. 如果剩余未摘要Token仍超过阈值，再发布下一任务

任务执行期间新增的日志不会进入本次快照，继续作为最近原始历史保留。

### 14.3 摘要模型

本阶段复用当前ChatModel，不新增独立模型配置。摘要调用设置独立超时，并要求模型通过原生Function Calling或严格JSON输出返回固定Schema。

输入内容：

```text
旧SummaryJSON
+ 本次压缩范围内的Question和Answer
+ AnswerMode
+ 必要的来源ChatLog ID
```

不向摘要模型传递完整`AgentTrace`、完整`RetrievalTrace`或文档Chunk正文，避免无关Token膨胀。

### 14.4 幂等和并发

- 重复消息必须通过`summary_task_id`幂等处理
- 新问答只增加`UnsummarizedTokens`，不修改`SummaryVersion`
- 摘要更新必须校验`BaseSummaryVersion`
- 更新失败时重新读取状态，不允许旧摘要覆盖新摘要
- 扣减Token使用数据库原子表达式，避免覆盖任务期间新增的Token
- 租约有效期内重复消费者进入延迟重试，不增加业务失败次数

### 14.5 失败处理

- 普通失败按照RabbitMQ现有延迟队列机制重试
- 超过最大重试次数后保留`failed`状态和错误信息
- 新问答成功落库后，如果会话仍超过阈值，可以重置任务并再次尝试发布
- RabbitMQ关闭或不可用时只记录告警，不阻断Agent请求
- Assembler持续使用硬预算裁剪降级

## 15. Feature Flag和旧链路

默认配置：

```yaml
agent_memory:
  short_term_enabled: true
```

灰度期间：

- `true`：使用Assembler、Conversation和异步摘要，不暴露`context_lookup`
- `false`：回退到现有`context_lookup`和二次Planner流程

旧链路只作为临时回退实现。新链路稳定后应删除：

- `ActionContextLookup`
- `planWithConversationContext`
- 旧的最近5轮和字符裁剪常量
- Planner中的`context_lookup`工具定义和提示词
- 相关归一化分支和旧测试

旧数据库会话不做回填。部署新版本后，只有存在`conversations`记录的新会话可以使用新链路；历史`conversation_id`统一视为无效。

## 16. Trace和可观测性

AgentTrace增加以下元数据，不记录摘要正文：

```json
{
  "conversation_id": "conv_xxx",
  "context_loaded": true,
  "context_summary_version": 2,
  "context_summarized_through_id": 105,
  "context_message_count": 6,
  "context_estimated_tokens": 3280,
  "context_degraded": false,
  "context_degraded_reason": "",
  "context_matched_topic": "topic_2",
  "conversation_answer_source_log_ids": [108]
}
```

服务指标建议：

- 上下文组装次数和耗时
- 上下文Token分布
- 硬预算裁剪次数
- 摘要任务发布、成功、失败和重试次数
- 摘要LLM耗时和Token
- 摘要前后压缩率
- 会话忙冲突次数
- 过期会话访问次数
- `conversation_answer`调用和回退次数

## 17. 代码改造建议

### 17.1 新增文件

```text
internal/model/conversation相关模型定义
internal/repository/conversation_repository.go
internal/service/conversation_context_assembler.go
internal/service/conversation_compactor.go
internal/tasks/conversation_compact相关Producer和Consumer
internal/agent/conversation_summary.go
```

模型目前集中在`internal/model/models.go`，实现时可以继续放在该文件，避免仅为了新功能重组无关代码。

### 17.2 修改文件

```text
internal/config/config.go
config.yaml.example
internal/database/database.go
internal/model/models.go
internal/repository/chat_log_repository.go
internal/service/agent_service.go
internal/service/chat_service.go
internal/service/errors.go
internal/handler/errors.go
internal/agent/planner.go
internal/agent/decision.go
internal/agent/llm_planner.go
internal/agent/trace.go
internal/tasks/messages.go
internal/tasks/producer.go
internal/tasks/consumers.go
cmd/server/main.go
README.md
```

### 17.3 Repository新增能力

`ConversationRepository`至少提供：

```text
CreateWithFirstLog
FindOwnedActive
TryAcquireProcessingLease
ReleaseProcessingLease
AppendLogAndRefresh
TryQueueCompaction
TryAcquireCompactionLease
CompleteCompaction
FailCompaction
MarkExpired
```

`ChatLogRepository`至少增加：

```text
ListAfterIDByConversation
ListRangeByConversation
FindManyOwnedByConversation
FindLatestIDByConversation
```

所有会话查询必须同时过滤`user_id`、`knowledge_base_id`和`conversation_id`。

## 18. 实施顺序

1. 增加配置、Conversation模型和数据库索引
2. 增加Repository和事务写入能力
3. 改造会话创建、归属校验、过期和请求租约
4. 实现Token估算和ConversationContextAssembler
5. 接入新Planner前置上下文链路
6. 增加`conversation_answer`
7. 增加结构化摘要生成和校验
8. 增加RabbitMQ摘要任务、租约、重试和降级
9. 增加Trace和指标
10. 保留Feature Flag旧链路并完成灰度验证
11. 更新README和配置示例
12. 稳定后删除旧`context_lookup`实现

## 19. 测试要求

### 19.1 Assembler单元测试

- 无摘要且未超过预算时返回完整历史
- 有摘要时只读取摘要游标之后的历史
- 超过硬预算时优先保留最新完整问答
- 裁剪后消息仍按时间正序
- 单条超长回答生成受控预览
- 用户、知识库或会话不匹配时拒绝
- 过期会话返回`ErrConversationExpired`
- RabbitMQ和摘要状态不影响正常读取

### 19.2 摘要测试

- 第一次摘要保留最近原始历史
- 增量摘要只处理游标之后的指定范围
- 用户纠正能够覆盖旧实体
- 新话题不会污染旧话题
- 返回旧话题能够更新`active_topic`
- 无效JSON不覆盖已有摘要
- 非当前会话的来源日志ID被拒绝
- 重复消息不会重复压缩
- 任务期间新增日志不会被错误扣减Token
- 摘要版本冲突不会覆盖新摘要

### 19.3 会话并发测试

- 同一会话第二个并发请求返回409
- 不同会话可以并行处理
- 请求失败后租约释放
- 进程异常后租约过期可重新抢占
- 非持有者不能释放其他请求的租约

### 19.4 conversation_answer测试

- “简短一点”复用上一轮原回答
- 格式转换继承并去重原Sources
- 新版本问题不能选择`conversation_answer`
- 来源日志越权时拒绝
- 来源日志不存在时回退检索或澄清
- Trace记录来源日志ID但不暴露摘要正文

### 19.5 集成场景

至少覆盖以下连续对话：

1. 短会话完整上下文追问
2. 超过阈值后摘要和最近消息共同参与Planner
3. 摘要任务延迟时硬预算裁剪
4. RabbitMQ关闭时主问答正常工作
5. 同一会话从报销话题切换到产品权限，再返回报销话题
6. 澄清问题经过摘要后仍能接续
7. 复述既有知识库回答并继承来源
8. 对既有回答提出新事实问题并重新触发RAG
9. 7天后继续会话返回410
10. Feature Flag关闭后回退旧链路

## 20. 验收标准

- 短会话不再固定截断为最近5轮
- 已有会话每轮Planner都能获得受预算控制的上下文
- 主问答链路不因摘要增加同步LLM调用
- 摘要异常不会导致Agent接口失败
- 同一会话不存在无序并发写入
- 摘要能够保留多话题、关键实体、约束和待澄清项
- 摘要正文不出现在AgentTrace响应中
- `conversation_answer`只变换既有内容并继承来源
- 新企业事实判断仍然走知识库检索
- 旧会话不回填且新会话不受旧日志影响
- Feature Flag可以快速回退到旧实现
