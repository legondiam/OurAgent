# Agent长期记忆实现说明

## 1. 背景

当前Agent已经具备会话级短期记忆：已有会话在Planner执行前自动装配结构化摘要和最近原始问答，历史过长时通过RabbitMQ异步压缩。短期记忆解决的是同一`conversation_id`内的连续交流，但会话过期或用户开启新会话后，用户仍需要重复说明角色、持续项目、业务对象和个人术语。

当前知识库相关性判断由Planner、`knowledge_probe`和服务端`ProbeEvidence`共同完成。`ProbeEvidence`中的业务主题和别名目前部分硬编码，无法理解特定用户在跨会话中形成的表达习惯，例如“客户环境”“老同步器”“上次那个项目”。

长期记忆用于跨会话保留这些稳定背景，帮助Agent解析业务对象、判断潜在知识库相关性并生成完整检索Query。长期记忆不是企业知识库的替代品，也不能把历史回答升级为当前有效的企业事实。

## 2. 业务目的

长期记忆的核心目的定义为：

> 跨会话保存用户稳定的工作上下文和表达习惯，帮助Agent识别用户所指对象、判断潜在知识库相关性并补全检索Query，同时保证企业事实仍由知识库提供证据。

重点支持以下企业知识工作场景：

- 用户角色和职责范围，例如区域、岗位、负责流程和主要客户类型
- 持续业务对象，例如正在跟进的客户、项目、系统和问题域
- 项目背景和稳定约束，例如部署形态、当前阶段和环境边界
- 用户或团队个人术语，例如简称、别名和特定指代
- 稳定协作偏好，例如先给制度依据、区分标准流程和例外情况

长期记忆首先服务于问题理解和检索，不以“保存更多聊天内容”为目标。

## 3. 非目标

- 不把产品价格、套餐限制、制度条款、权限规则、API参数和版本能力保存为可直接回答的企业事实
- 不把全部历史会话向量化后当作长期记忆
- 不实现历史会话全文搜索，历史搜索应单独基于`chat_logs`设计
- 不从助手单方面生成的回答中提取长期事实
- 不保存密码、Token、API Key、私钥、客户原始数据等敏感信息
- 不实现跨用户、跨租户共享记忆
- 不允许用户记忆覆盖安全、权限和实时性等系统规则
- 本阶段不自动生成完整`KnowledgeBaseProfile`
- 本阶段不删除或重写短期记忆实现
- 不回填上线前的历史`chat_logs`

## 4. 核心决策

### 4.1 记忆不是Agent Tool

长期记忆属于Agent生命周期内部组件，不向Planner暴露`memory_read`、`memory_write`或`memory_forget`工具。

- 读取在Planner之前自动执行
- 普通对话的候选提取在问答保存后异步执行
- 显式“记住、纠正、忘掉”由内部`MemoryDirectiveProcessor`处理
- Planner只能消费已经组装好的记忆上下文，不能自行决定是否读写记忆

该设计避免模型漏读记忆、随意写入记忆以及记忆动作挤占正常业务动作。

### 4.2 MySQL存真值，Qdrant存检索索引

MySQL保存：

- 用户和知识库归属
- 记忆类型、作用域和结构化身份
- 当前内容、状态、版本和有效期
- 来源日志和用户证据
- 冲突、覆盖、删除和遗忘屏障
- Embedding和异步任务状态

Qdrant只保存Embedding及用于候选过滤的Payload。任何Qdrant命中都必须回查MySQL，只有当前仍为`active`且未过期的记忆才能注入Planner。

### 4.3 固定加载与语义召回并存

并非所有长期记忆都依赖向量检索：

- 少量由用户明确确认的全局协作偏好按结构化条件固定加载
- 个人术语优先进行标准化词面匹配
- 项目背景、角色、持续业务对象和其他上下文只在`MemoryRecallGate`命中后通过Embedding语义召回

语义召回解决相关候选发现，确定性条件保证稳定偏好不会因向量分数波动而遗漏。普通知识查询不调用长期记忆Embedding，也不查询记忆Qdrant Collection。

### 4.4 只信任可归因于用户的证据

MemoryConsolidator可以读取最近上下文来解析指代，但只允许提取用户明确表达、确认或纠正的内容。助手回答和RAG结果不能单独成为长期记忆来源。

### 4.5 企业事实类型排除

产品能力、套餐限制、价格、制度条款、权限规则、API参数、版本能力和其他公共企业事实不属于长期记忆允许表达的类型。无论内容是否准确、用户是否明确要求“记住”，都不能写入Memory、Evidence或Qdrant。

系统不判断这类事实是否正确，只判断它是否属于长期记忆的业务范围。显式指令命中企业事实类型时，返回“该信息不作为长期记忆保存，后续仍以知识库为准”，不能静默转换成用户兴趣。

长期记忆可以提供：

- 用户所说的“客户环境”指哪个项目环境
- 用户负责哪个区域或项目
- 用户关注哪个产品、流程或问题域
- 用户希望从哪个业务视角获得答案

长期记忆不能证明：

- 某套餐当前允许多少成员
- 某制度当前是否仍有效
- 某版本是否支持某项能力
- 某权限或审批条件是否已经变化

涉及当前版本、有效性、适用范围、例外、价格、政策或其他企业事实时，Planner仍必须选择`knowledge_probe`或`knowledge_search`。

长期记忆可以在回答中明确归因为“用户此前提供的背景”。回顾用户以前表达时可以直接复述；将背景用于当前任务时应作为前提；确认当前客观状态时必须重新查询权威来源或请用户确认，不能把旧记忆表述为当前事实。

### 4.6 KnowledgeBaseProfile边界

长期记忆保存用户特有背景；知识库画像描述知识库对所有用户共有的产品、主题、版本和公共别名。

```text
UserLongTermMemory：这个用户通常在说什么、负责什么
KnowledgeBaseProfile：这个知识库覆盖什么业务对象和主题
```

本阶段只为`KnowledgeBaseProfile`预留上下文融合接口，不实现文档级画像自动提取。当前通用安全、实时、模糊指代和业务路由规则继续保留；后续可将知识库公共别名和主题逐步迁移到独立画像能力。

## 5. 总体架构

```text
Agent请求
  |
  +-- 校验用户和知识库归属
  |
  +-- MemoryDirectiveMatcher
  |     |
  |     +-- 无显式指令：继续
  |     +-- 有显式指令：MemoryDirectiveProcessor
  |              |
  |              +-- 记住/纠正/遗忘
  |              +-- MySQL立即生效
  |              +-- 创建异步索引或删除任务
  |
  +-- 并行上下文读取
  |     |
  |     +-- ConversationContextAssembler
  |     +-- LongTermMemoryRetriever
  |     |     |
  |     |     +-- 固定偏好和个人术语词面匹配
  |     |     +-- MemoryRecallGate
  |     |     +-- 按需语义召回
  |     +-- KnowledgeBaseProfileProvider预留接口
  |
  +-- AgentContextAssembler
  |     |
  |     +-- 短期会话上下文
  |     +-- 相关长期记忆
  |     +-- Token预算和事实边界说明
  |
  +-- Planner
  |     |
  |     +-- direct_answer
  |     +-- conversation_answer
  |     +-- knowledge_probe
  |     +-- knowledge_search
  |     +-- clarify
  |     +-- web_search
  |     +-- reject
  |
  +-- 执行并保存chat_log
        |
        +-- MemoryWorthinessGate本地判断
        +-- 无价值：不创建任务
        +-- 潜在价值：写MemoryConsolidationSignal
        +-- 显式记忆指令：跳过重复提炼或只处理剩余业务内容

异步任务
  |
  +-- memory.consolidate
  |     +-- 同一会话候选信号批量提取
  |     +-- 合并证据
  |     +-- 升级或冲突判断
  |     +-- 创建memory.index任务
  |
  +-- memory.index
  |     +-- 生成Embedding
  |     +-- Upsert独立Qdrant Collection
  |
  +-- memory.delete
        +-- 删除Qdrant Point
```

## 6. 记忆类型与作用域

### 6.1 允许的记忆类型

```go
const (
	MemoryTypePreference     = "preference"
	MemoryTypeRole           = "role"
	MemoryTypeBusinessObject = "business_object"
	MemoryTypeProjectContext = "project_context"
	MemoryTypeTerminology    = "terminology"
	MemoryTypeInstruction    = "instruction"
)
```

类型含义：

- `preference`：稳定回答和表达偏好
- `role`：用户角色、职责和业务范围
- `business_object`：持续关注的客户、系统、产品或流程
- `project_context`：项目背景、阶段和稳定约束
- `terminology`：用户个人简称、别名和指代映射
- `instruction`：长期协作要求

服务端只接受白名单类型，不能信任模型自由生成的类型名。

### 6.2 作用域

```go
const (
	MemoryScopeUserGlobal    = "user_global"
	MemoryScopeKnowledgeBase = "knowledge_base"
)
```

默认作用域：

| 类型 | 默认作用域 |
|---|---|
| preference | user_global，但必须由用户明确确认 |
| role | knowledge_base |
| business_object | knowledge_base |
| project_context | knowledge_base |
| terminology | knowledge_base |
| instruction | knowledge_base |

`user_global`记录的`knowledge_base_id`必须为`NULL`；`knowledge_base`记录必须包含当前用户有权限访问的知识库ID。

`preference`不参与普通批量提取，只能由用户明确要求记住并保存为`user_global`。其他类型只能保存为`knowledge_base`。系统不能自动将知识库内记忆提升为全局，也不支持一条记忆跨多个知识库共享。

### 6.3 状态与持久性

状态：

```text
candidate
active
conflicted
superseded
expired
deleting
```

持久性：

```text
stable
temporary
```

状态表示记忆是否可信可用；持久性决定默认有效期。两个维度不能混用。

只有`active`且未过期的记忆可以进入Planner。

## 7. 数据模型

### 7.1 LongTermMemory

`long_term_memories`每个结构化身份只保留一行当前状态，唯一键防止后台任务并发创建多个当前记忆。

```go
type LongTermMemory struct {
	ID                 uint64         `gorm:"primaryKey" json:"id"`
	UserID             uint64         `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID    *uint64        `gorm:"index" json:"knowledge_base_id,omitempty"`
	Scope              string         `gorm:"size:32;index;not null" json:"scope"`
	Type               string         `gorm:"size:32;index;not null" json:"type"`
	MemoryKey          string         `gorm:"size:255;not null" json:"memory_key"`
	IdentityHash       string         `gorm:"size:64;uniqueIndex;not null" json:"identity_hash"`
	Subject            string         `gorm:"size:255;index;not null" json:"subject"`
	Attribute          string         `gorm:"size:128;not null" json:"attribute"`
	Value              string         `gorm:"type:text;not null" json:"value"`
	Content            string         `gorm:"type:text;not null" json:"content"`
	Status             string         `gorm:"size:32;index;not null" json:"status"`
	Durability         string         `gorm:"size:32;not null" json:"durability"`
	Confidence         float64        `json:"confidence"`
	Importance         float64        `json:"importance"`
	EvidenceCount      int            `gorm:"not null;default:0" json:"evidence_count"`
	ConversationCount  int            `gorm:"not null;default:0" json:"conversation_count"`
	Version            uint64         `gorm:"not null;default:1" json:"version"`
	EmbeddingStatus    string         `gorm:"size:32;index;not null" json:"embedding_status"`
	EmbeddingModel     string         `gorm:"size:100" json:"embedding_model"`
	EmbeddingHash      string         `gorm:"size:64" json:"embedding_hash"`
	VectorID           string         `gorm:"size:128;index" json:"vector_id"`
	FirstObservedAt    time.Time      `json:"first_observed_at"`
	LastConfirmedAt    *time.Time     `gorm:"index" json:"last_confirmed_at"`
	LastUsedAt         *time.Time     `json:"last_used_at"`
	ExpiresAt          *time.Time     `gorm:"index" json:"expires_at"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}
```

`IdentityHash`按以下字段规范化后计算SHA-256：

```text
user_id
scope
knowledge_base_id或0
type
normalized_subject
normalized_attribute
```

`Content`是注入Planner的自然语言表达；`Subject`、`Attribute`和`Value`用于冲突、覆盖和精确匹配。

### 7.2 LongTermMemoryVersion

`long_term_memory_versions`为每个已提交版本保存不可变快照，用于审计和问题排查。创建记忆时写入版本1；修改时更新当前行并写入新版本快照，旧版本快照保持不变。

```go
type LongTermMemoryVersion struct {
	ID             uint64         `gorm:"primaryKey" json:"id"`
	MemoryID       uint64         `gorm:"index;not null" json:"memory_id"`
	Version        uint64         `gorm:"not null" json:"version"`
	SnapshotJSON   datatypes.JSON `gorm:"type:json;not null" json:"snapshot_json"`
	ChangeType     string         `gorm:"size:32;index;not null" json:"change_type"`
	SourceChatLogID *uint64       `gorm:"index" json:"source_chat_log_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}
```

建议唯一索引：

```text
(memory_id, version)
```

### 7.3 LongTermMemoryEvidence

`long_term_memory_evidences`记录记忆来自哪些用户表达，避免把模型置信度当作事实依据。

```go
type LongTermMemoryEvidence struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	MemoryID       uint64    `gorm:"index;not null" json:"memory_id"`
	UserID         uint64    `gorm:"index;not null" json:"user_id"`
	ConversationID string    `gorm:"size:64;index;not null" json:"conversation_id"`
	ChatLogID      uint64    `gorm:"index;not null" json:"chat_log_id"`
	EvidenceHash   string    `gorm:"size:64;not null" json:"evidence_hash"`
	EvidenceKind   string    `gorm:"size:32;not null" json:"evidence_kind"`
	Explicit       bool      `gorm:"not null" json:"explicit"`
	CreatedAt      time.Time `json:"created_at"`
}
```

建议唯一索引：

```text
(memory_id, chat_log_id, evidence_hash)
```

正文不重复保存在Evidence表，实际来源通过`chat_log_id`回查。API可以返回来源ID和时间，不默认返回完整聊天内容。

### 7.4 LongTermMemoryJob

为避免“数据库已更新但RabbitMQ消息未发布”的崩溃窗口，所有异步动作先在事务中创建数据库任务，再由调度器发布。

```go
type LongTermMemoryJob struct {
	ID             string         `gorm:"size:64;primaryKey" json:"id"`
	Type           string         `gorm:"size:32;index;not null" json:"type"`
	UserID         uint64         `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID *uint64       `gorm:"index" json:"knowledge_base_id,omitempty"`
	MemoryID       *uint64        `gorm:"index" json:"memory_id,omitempty"`
	ChatLogID      *uint64        `gorm:"index" json:"chat_log_id,omitempty"`
	PayloadJSON    datatypes.JSON `gorm:"type:json" json:"payload_json"`
	Status         string         `gorm:"size:32;index;not null" json:"status"`
	Attempt        int            `gorm:"not null;default:0" json:"attempt"`
	LeaseUntil     *time.Time     `gorm:"index" json:"lease_until"`
	LastError      string         `gorm:"type:text" json:"last_error"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}
```

`PayloadJSON`只能保存Signal ID、Memory ID、版本、EmbeddingHash和删除范围等控制信息，不能复制记忆正文或用户消息，避免用户遗忘后任务表残留内容。

任务类型：

```text
consolidate
index
delete_vector
expire
```

`consolidate`对`chat_log_id`建立唯一约束，保证一个已完成问答只巩固一次。

### 7.5 LongTermMemoryForgetTombstone

用户遗忘后必须阻止旧的延迟任务重新激活已删除内容。

```go
type LongTermMemoryForgetTombstone struct {
	ID                    uint64    `gorm:"primaryKey" json:"id"`
	UserID                uint64    `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID       *uint64   `gorm:"index" json:"knowledge_base_id,omitempty"`
	IdentityHash          string    `gorm:"size:64;index" json:"identity_hash"`
	SubjectHash           string    `gorm:"size:64;index" json:"subject_hash"`
	ForgetBeforeChatLogID uint64    `gorm:"not null" json:"forget_before_chat_log_id"`
	CreatedAt             time.Time `json:"created_at"`
}
```

精确遗忘写`IdentityHash`；按主题遗忘写规范化后的`SubjectHash`。Tombstone不能保存记忆正文或可直接识别的主体明文。来源日志不晚于屏障游标的任务不得恢复记忆，只有屏障之后的新用户证据才能创建新记忆。

### 7.6 MemoryConsolidationSignal

每轮问答不直接创建LLM提取任务。无模型`MemoryWorthinessGate`只为具有潜在长期价值的用户消息创建轻量信号。

```go
type MemoryConsolidationSignal struct {
	ID                 uint64    `gorm:"primaryKey" json:"id"`
	UserID             uint64    `gorm:"index;not null" json:"user_id"`
	KnowledgeBaseID    uint64    `gorm:"index;not null" json:"knowledge_base_id"`
	ConversationID     string    `gorm:"size:64;index;not null" json:"conversation_id"`
	ChatLogID          uint64    `gorm:"uniqueIndex;not null" json:"chat_log_id"`
	SignalType         string    `gorm:"size:32;index;not null" json:"signal_type"`
	SignalSource       string    `gorm:"size:32;not null" json:"signal_source"`
	EstimatedTokens    int       `gorm:"not null;default:0" json:"estimated_tokens"`
	Status             string    `gorm:"size:32;index;not null" json:"status"`
	TaskID             string    `gorm:"size:64;index" json:"task_id"`
	EligibleAt         time.Time `gorm:"index;not null" json:"eligible_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
```

状态包括`pending`、`queued`、`processing`、`completed`和`cancelled`。调度器只按同一用户、知识库和会话聚合`pending`信号，不跨用户、知识库或会话合并Prompt。

## 8. 记忆身份、合并和冲突

### 8.1 结构化身份

MemoryConsolidator输出：

```json
{
  "type": "project_context",
  "subject": "星河项目",
  "attribute": "deployment_mode",
  "value": "私有化部署",
  "content": "星河项目采用私有化部署",
  "scope": "knowledge_base",
  "durability": "temporary",
  "confidence": 0.86,
  "importance": 0.8,
  "explicit": false,
  "source_chat_log_ids": [108]
}
```

服务端重新规范化类型、作用域、主体和属性，并计算`IdentityHash`。模型不能提供数据库ID、用户ID或知识库ID。

### 8.2 重复证据

相同`IdentityHash`且值语义等价时：

- 不创建第二条记忆
- 新增唯一Evidence
- 更新`EvidenceCount`
- 按不同`conversation_id`重算`ConversationCount`
- 用户再次确认时刷新`LastConfirmedAt`和`ExpiresAt`
- 仅被系统召回不能刷新有效期

### 8.3 明确覆盖

相同身份出现新值且用户表达明确时：

1. 使用`SELECT FOR UPDATE`锁定当前记忆
2. 写入旧快照到Version表
3. 更新当前内容和值
4. `Version+1`
5. 状态设为`active`
6. 创建新的Index任务

例如“项目已经结束POC，现在进入正式交付”覆盖旧项目阶段。

### 8.4 不确定冲突

如果新表达包含“可能、好像、也许”等不确定性，或者术语在同一作用域映射到多个业务对象：

- 不覆盖当前`active`
- 将当前记忆或新候选标记为`conflicted`
- `conflicted`不进入Planner
- 等待用户明确确认或通过管理API处理

不同知识库或不同项目范围下的同名术语可以共存。

### 8.5 作用域优先级

```text
当前知识库 > 用户全局
明确用户确认 > 自动升级
较新确认 > 较旧确认
```

更具体作用域只覆盖当前请求中的选择，不删除全局记忆。

## 9. 候选提取与升级

### 9.1 提取时机

普通问答保存前先运行不调用模型的`MemoryWorthinessGate`。它只识别通用的稳定背景表达结构和记忆管理信号，不维护具体产品、制度和业务主题词表。

```text
none：普通查询或当前任务指令，不创建Signal
candidate：可能包含跨会话背景，创建Signal
explicit：交给MemoryDirectiveProcessor
```

只有以下条件同时满足时才写`MemoryConsolidationSignal`：

- 长期记忆Feature Flag开启
- `chat_log`已经成功提交
- 本轮不是系统错误
- 本轮不是`reject`
- 本轮不是纯显式记忆指令，或仍有未处理的业务内容
- 当前用户和知识库仍有效
- Gate判断存在明显的角色、持续业务对象、项目背景、术语或纠正信号

普通制度、流程、产品和配置查询不创建Signal，也不调用记忆LLM。

调度器按同一会话批量处理Signal：

- 最后一条候选信号后空闲5分钟
- 或待处理候选达到10条
- 或待处理候选达到4000估算Token

达到任一条件后固定Signal ID快照并创建一次`consolidate`任务。用户关闭会话框或浏览器不影响数据库调度；服务重启后继续扫描到期Signal。

任务异步执行，不增加普通聊天接口的LLM调用次数。

### 9.2 提取输入

Consolidator每次只处理同一用户、知识库和会话中的一批候选Signal，使用：

- 最多10条候选Signal对应的用户问题
- 每条候选前后为解析指代所需的最少上下文
- 必要的短期摘要
- 当前用户在该知识库中的相关有效记忆
- 当前候选和遗忘屏障

没有候选Signal的普通消息不进入批量Prompt。助手回答只能帮助解析“对，就是这个”等指代，不能作为独立证据。Prompt必须要求每条输出都能定位到用户消息中的表达或确认。

### 9.3 提取方式

复用当前ChatModel，使用原生Function Calling和强约束Schema输出候选数组。服务端限制：

- 每批最多5个候选
- `source_chat_log_ids`必须属于当前用户和会话
- `type`、`scope`、`durability`必须在白名单
- `subject`、`attribute`、`value`和`content`不能为空
- 不接受密钥、凭据和客户原始数据
- 不接受只存在于助手回答中的结论
- 不接受产品能力、套餐限制、价格、制度条款、权限规则、API参数或版本能力等企业事实类型
- 每条候选必须返回一段用户原文证据，且服务端能在对应`chat_log.question`中验证该子串

批次超过10条候选或4000 Token时按时间顺序拆分。前一批提交后，后一批可以读取已经产生的Memory并进行合并或冲突判断。

### 9.4 自动升级

只有显式Directive可以直接进入`active`：

- 用户明确说“记住、以后、后续都按”等长期指令
- 用户明确纠正已有记忆

普通对话中的以下内容全部先进入`candidate`：

- 用户角色和职责陈述
- 用户术语映射
- 普通项目背景
- 当前持续业务对象
- 可能跨会话使用的项目约束

候选自动升级必须满足：

- 至少2个不同`conversation_id`提供一致证据
- 最近证据仍在候选有效期内
- 不存在冲突
- 不包含推测性表达
- 服务端结构、安全和来源检查通过

允许跨会话自动升级的类型仅限：

- `role`
- `business_object`
- `project_context`，其中阶段等易变属性使用较短TTL
- 无冲突的`terminology`

`preference`只能由用户明确要求保存，不进入普通批量提取；`instruction`必须由用户明确要求或通过管理入口确认，不能因重复出现自动升级。当前会话中的用户决策由短期上下文承接；正式决策、审批结果和执行状态应来自知识库或业务系统，不属于长期记忆类型。

模型置信度不能单独触发升级。

通过Consolidator检查的`candidate`需要异步向量化。Agent召回只搜索`active`；Consolidator去重和跨会话匹配可以搜索`candidate + active`。

### 9.5 候选过期

未升级候选默认30天过期。过期后状态变为`expired`，不再参与合并候选搜索；保留记录用于审计，后续可以批量物理清理。

## 10. 显式记忆指令

### 10.1 内部组件

显式指令由以下内部组件处理：

```go
type MemoryDirectiveMatcher interface {
	MayContainDirective(text string) bool
}

type MemoryDirectiveProcessor interface {
	Process(ctx context.Context, req MemoryDirectiveRequest) (MemoryDirectiveResult, error)
}
```

它们不是Eino Tool，也不加入Planner的可用Function列表。

### 10.2 两阶段识别

第一阶段为无模型轻量Matcher，只判断是否出现明显记忆管理信号，例如：

```text
记住
以后
后续都按
我们说的X是Y
不是X，是Y
我不再负责
忘掉
不要再记得
```

Matcher只用于减少额外模型调用，不能直接修改数据。

第二阶段仅在Matcher命中时调用ChatModel，通过强约束Schema解析：

```text
operation：remember / correct / forget / none
memory_type
scope
subject
attribute
value
content
target_memory_ids
residual_question
requires_confirmation
excluded_reason
```

这是普通聊天链路唯一允许增加同步记忆模型调用的情况。V1直接复用当前ChatModel，不新增独立模型配置。

### 10.3 执行语义

- 纯记忆指令：准备确定性确认文本，最后在同一事务中保存`chat_log`、Memory、Version、Evidence和异步任务
- 复合指令：将解析出的记忆内容作为本轮临时上下文交给`residual_question`的正常Planner，回答完成后在同一事务中保存问答和记忆变更
- 企业事实类型：返回`excluded_enterprise_fact`，不写数据库、不生成Embedding，并说明后续仍以知识库为准
- 除用户明确确认的全局偏好外，写入作用域强制限定当前知识库
- 解析失败：不得声称已经记住，返回澄清问题
- 保存事务失败：请求失败，不得返回成功确认或业务回答
- 发布Index任务失败：MySQL记忆仍生效，任务调度器后续恢复
- 删除全部记忆：聊天入口V1只返回需要到记忆管理接口确认，不直接批量删除

显式指令不能在`chat_log`生成之前单独写Evidence。建议新增统一的`SaveAgentTurnWithMemoryOperations`事务入口，先创建`chat_log`获得ID，再用该ID写Memory、Version、Evidence和Job，任一步失败都整体回滚。

### 10.4 遗忘

“忘掉长期记忆”表示最终彻底删除派生记忆内容，不是永久软删除。由于MySQL和Qdrant无法组成同一个事务，采用两阶段删除。

第一阶段在MySQL事务中：

1. 锁定目标记忆
2. 状态更新为`deleting`，从当前请求开始禁止召回
3. 取消相关候选Signal、Consolidate和Index任务
4. 写只含哈希和日志游标的ForgetTombstone
5. 创建不含正文的`delete_vector`任务
6. API返回`deletion_pending`

第二阶段由Delete Consumer完成：

1. 幂等删除Qdrant Point
2. 在MySQL事务中删除Memory、全部Version和Evidence正文
3. 删除或完成相关任务
4. 记录不含原文的删除完成事件

Qdrant删除失败时记忆保持`deleting`并继续重试。该状态不进入Planner，但系统不能声称物理删除已经完成。Tombstone不能保存原始主体或正文，只用于阻止旧异步任务根据历史日志重新生成记忆。

删除长期记忆不修改原始`chat_logs`。管理界面和API必须说明“删除记忆不会删除聊天记录”；如果用户还需要删除原始聊天，应使用会话删除能力。

### 10.5 删除会话

用户主动删除会话时：

- 取消该会话尚未处理的Memory Signal和Consolidate任务
- 已经激活的长期记忆默认保留
- 提供“删除会话并遗忘由该会话产生的长期记忆”选项
- 选择同时遗忘时，仅删除Evidence全部来自该会话的记忆；多会话共同确认的记忆需要明确展示并再次确认

用户关闭会话框、离开页面或会话自然过期不等于删除会话，后台仍处理已经提交的候选Signal。

## 11. 有效期

建议默认值：

| 类型 | 默认有效期 |
|---|---:|
| preference | 180天 |
| role | 180天 |
| terminology | 180天 |
| business_object | 90天 |
| project_context稳定背景 | 90天 |
| project_context阶段状态 | 30天 |
| candidate | 30天 |

所有值必须可配置。

只有新的用户证据可以刷新`LastConfirmedAt`和`ExpiresAt`。`LastUsedAt`只用于可观测性和排序，不能延长有效期，避免错误记忆因系统反复使用而永久存活。

过期任务将`active`或`candidate`更新为`expired`并创建向量删除任务。V1不立即物理删除过期记录。

## 12. Qdrant设计

### 12.1 独立Collection

新增独立集合：

```text
our_agent_memories
```

不能复用当前文档Chunk集合，原因包括：

- 文档和记忆生命周期不同
- Payload、权限过滤和删除条件不同
- 文档检索结果会作为企业证据，记忆不会
- 当前`QdrantClient`假设结果包含`chunk_id`和`document_id`

新增`MemoryVectorStore`，不直接复用面向文档Chunk的Search结果类型。

### 12.2 Point和Payload

Point ID使用`LongTermMemory.ID`。

```json
{
  "memory_id": 101,
  "user_id": 8,
  "knowledge_base_id": 12,
  "scope": "knowledge_base",
  "memory_type": "project_context",
  "status": "active",
  "version": 3
}
```

Qdrant Payload不保存记忆正文或主体明文。Payload可能因异步更新短暂滞后，因此它只用于初步过滤，MySQL状态始终为最终判断。

### 12.3 Embedding文本

Embedding输入使用规范化组合文本：

```text
记忆类型：project_context
主体：星河项目
属性：deployment_mode
内容：星河项目采用私有化部署
```

术语记忆同时包含别名和标准对象。Embedding模型复用当前配置的Embedding模型，模型变化时通过`EmbeddingModel`和`EmbeddingHash`识别并重建索引。

### 12.4 候选索引

`active`和`candidate`都可以写入Collection：

- 面向Agent的检索只搜索`active`
- Consolidator合并候选时可搜索`active`和`candidate`

最终都必须回查MySQL。`superseded`和`expired`应异步删除向量；用户遗忘时先进入`deleting`阻断使用，Qdrant删除成功后再彻底清除MySQL派生正文。

## 13. 长期记忆检索

### 13.1 输入

```go
type LongTermMemoryRetrieveRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
	CurrentTopic    string
}
```

### 13.2 三路召回

```text
固定召回
  +-- 用户明确确认的少量全局preference

词面召回
  +-- terminology.subject出现在当前问题
  +-- 已知业务对象名称精确或标准化匹配

按需语义召回
  +-- MemoryRecallGate命中
  +-- Embedding当前问题和当前话题
  +-- Qdrant TopK
  +-- 当前用户和当前知识库严格过滤
```

三路结果按Memory ID去重后批量回查MySQL。

### 13.3 MemoryRecallGate

长期记忆语义召回不在每轮执行。无模型`MemoryRecallGate`根据以下信号按需开启：

- “上次、之前、继续”等跨会话表达
- “这个项目、那个客户、客户环境”等缺少当前会话来源的业务指代
- “我负责的、我正在跟进的”等用户背景表达
- 当前问题命中已知个人术语或业务对象
- 短期上下文不足以解析的明显指代

Gate未命中时不调用长期记忆Embedding，也不查询记忆Qdrant Collection。Gate采用保守策略，允许漏掉隐晦关联，避免大量普通企业查询增加固定模型调用。

同一请求内对相同文本生成的Embedding需要缓存；如果后续Knowledge Probe或RAG使用相同Query，可以复用，避免重复请求Embedding服务。

### 13.4 权限过滤

允许返回：

```text
user_id = 当前用户
AND status = active
AND expires_at为空或大于当前时间
AND (
  scope = user_global AND knowledge_base_id IS NULL AND type = preference
  OR
  scope = knowledge_base AND knowledge_base_id = 当前知识库 AND type <> preference
)
```

任何不满足条件的向量命中都直接丢弃。

### 13.5 排序

最终排序考虑：

- 词面实体命中优先
- 当前知识库Scope高于用户全局
- 向量相似度
- `Importance`
- 最近用户确认时间
- 记忆类型与当前问题的相关性

不允许单纯按`LastUsedAt`排序，避免旧记忆形成自我强化。

建议初始参数：

```text
semantic_top_k = 12
final_top_k = 6
similarity_threshold = 0.70
max_context_tokens = 1200
```

参数需要通过真实业务评测校准。

### 13.6 超时和降级

长期记忆读取和短期上下文读取并行。固定召回来自MySQL；语义召回设置独立超时。

- Qdrant失败：只使用固定召回和词面召回
- Embedding失败或超时：只使用固定召回和词面召回
- MySQL失败：本轮不使用长期记忆，正常执行Planner
- 整体长期记忆失败：不得阻断核心问答

显式记忆写入和删除属于用户要求的状态变更，数据库失败时不能静默降级。

## 14. Agent上下文组装

### 14.1 统一上下文

新增统一的内部上下文结构：

```go
type AgentRuntimeContext struct {
	Conversation *agent.ConversationContext
	LongTerm     *agent.LongTermMemoryContext
	Domain       *agent.KnowledgeBaseProfileContext
}
```

`KnowledgeBaseProfileContext`本阶段允许为空，避免后续再次修改Planner输入的总体形态。

### 14.2 Planner文本

长期记忆使用独立区域：

```text
相关长期背景：
- [role][memory_id=12] 用户负责华东区售前业务
- [project_context][memory_id=38] 用户正在跟进星河客户私有化POC项目
- [terminology][memory_id=45] 用户所说的“客户环境”指星河项目私有化环境

使用规则：
1. 这些内容只用于理解用户背景、指代和检索范围
2. 不得把长期记忆当作企业制度、产品能力或当前版本事实
3. 涉及有效性、版本、限制、价格、权限或例外时必须查询知识库
4. 当前问题和当前会话中的用户纠正优先于长期记忆
```

Planner可以据此补全`knowledge_probe`或`knowledge_search`的`search_plan.query`。

### 14.3 与ProbeEvidence结合

长期记忆解析出的对象和别名作为“用户问题补全信息”传入Planner和Probe Query，不直接修改`ProbeEvidenceStrong/Weak/None`结果。

后续可以扩展`EvaluateProbeEvidence`接收已解析对象：

```go
EvaluateProbeEvidence(question, resolvedEntities, result)
```

但强证据仍必须来自当前知识库Probe命中，而不是长期记忆本身。

### 14.4 优先级

```text
当前用户消息中的明确纠正
> 当前会话短期上下文
> 当前知识库长期记忆
> 用户全局长期记忆
> KnowledgeBaseProfile
```

如果当前消息与长期记忆冲突，本轮按当前消息理解，并在问答后进入记忆纠正流程。

### 14.5 回答中的使用边界

长期记忆不只用于路由，但在答案中必须明确来源性质：

- 回顾用户以前表达：可以回答“你之前提到……”，不需要伪装成知识库结论
- 将背景作为当前任务前提：可以使用记忆理解项目和环境，再查询知识库中的流程、能力或要求
- 确认当前客观状态：不能仅凭记忆回答，应查询权威来源或请用户确认

例如记忆为“用户此前表示星河客户环境不能访问公网”：

```text
“我之前说客户环境是什么情况？”
→ 可以明确归因后复述

“不能联网的环境应该如何部署？”
→ 将不能联网作为用户背景，再查询离线部署资料

“客户环境现在还能不能访问公网？”
→ 说明旧背景可能变化，查询权威来源或要求确认
```

长期记忆不能作为知识库Source返回，也不能生成伪造的文档引用。

## 15. 异步任务与可靠性

### 15.1 RabbitMQ拓扑

新增：

```text
memory.consolidate
memory.consolidate.retry
memory.consolidate.dlq

memory.index
memory.index.retry
memory.index.dlq

memory.delete
memory.delete.retry
memory.delete.dlq
```

任务消息只携带任务ID和必要实体ID，任务权威状态在MySQL。

### 15.2 数据库任务调度

写Memory变更和创建Index/Delete Job必须处于同一事务。保存普通问答时只在Gate命中后事务写入Signal，不直接创建Consolidate Job。

Signal调度器扫描：

- `EligibleAt`到期的同会话`pending`信号
- 同会话待处理信号达到10条
- 同会话待处理信号达到4000 Token

调度器在事务中锁定最多10条或4000 Token的Signal快照，将其标记为`queued`并创建唯一Consolidate Job。新产生的Signal留给下一批。

Job调度器扫描：

- 新建`queued`任务
- 发布后未及时消费且租约过期的任务
- 进程崩溃留下的`processing`任务

发布RabbitMQ成功后更新状态；发布失败保留`queued`并记录错误，下次继续调度。

### 15.3 租约和幂等

- 任务通过`task_id`和条件更新抢占租约
- 默认租约180秒
- 相同任务重复投递只允许一次提交
- Consolidate按固定Signal ID集合和任务ID幂等
- Evidence按`memory_id + chat_log_id + evidence_hash`幂等
- Index提交时校验Memory版本和EmbeddingHash
- Delete允许重复执行，Qdrant不存在视为成功
- 旧版本Index任务不能覆盖新版本向量

### 15.4 失败处理

- Consolidate失败：普通问答不受影响，延迟重试
- Index失败：结构化固定记忆仍可从MySQL加载，语义召回暂不可用
- Delete失败：Memory保持`deleting`且无法召回，后台继续重试；只有Qdrant和MySQL派生正文都删除后才标记完成
- 超过最大重试次数：进入DLQ并保留数据库`failed`状态
- Trace和日志不记录完整记忆正文

## 16. 管理接口

所有接口位于鉴权路由下，并按当前用户隔离。

携带`knowledge_base_id`的查询、确认、修改和删除操作必须先校验知识库归属，不能仅根据Memory ID判断权限。

### 16.1 查询

```text
GET /api/v1/memories
```

筛选参数：

```text
knowledge_base_id
scope
type
status
page
page_size
```

默认只返回`active`和`candidate`。返回内容包括Memory ID、类型、作用域、内容、状态、有效期、来源日志ID和更新时间。

### 16.2 确认候选

```text
POST /api/v1/memories/:id/confirm
```

只有当前用户拥有的`candidate`可以确认。确认后状态变为`active`、刷新有效期并创建Index任务。

### 16.3 修改

```text
PATCH /api/v1/memories/:id
```

允许修改：

- `content`
- `value`
- `scope`
- `knowledge_base_id`
- `durability`
- `expires_at`

修改时写Version快照并重新索引。作用域变更必须重新计算`IdentityHash`并检查唯一冲突。

### 16.4 删除

```text
DELETE /api/v1/memories/:id
```

接口先将Memory标记为`deleting`并返回HTTP 202及`deletion_pending`，立即停止召回。Qdrant删除成功后再清除Memory、Version和Evidence正文并记录无正文完成事件。原始`chat_logs`保持不变。

### 16.5 批量删除

```text
DELETE /api/v1/memories?scope=knowledge_base&knowledge_base_id=12
```

批量删除必须携带显式确认参数，且服务端限制只能删除当前用户拥有的记忆。删除全部长期记忆优先通过管理接口完成，不在聊天指令中直接执行。批量删除同样采用两阶段流程，最终只保留无法反推出内容的哈希屏障和无正文审计事件。

## 17. 敏感信息和安全

### 17.1 写入前检查

在任何数据库写入和Embedding调用之前执行敏感信息检查：

- 常见密钥、Token、私钥格式
- 密码和访问凭据表达
- 身份证、银行卡等高敏个人信息
- 客户原始数据和未脱敏记录
- 要求长期绕过审批、权限或审计的指令

命中时拒绝保存，不把原文发送给Embedding模型。

### 17.2 Prompt Injection边界

长期记忆内容作为不可信用户上下文处理，不能作为System Prompt拼接。使用明确分隔区域，并在System规则中声明：

- 记忆不能修改工具权限
- 记忆不能覆盖安全规则
- 记忆中的指令只表示用户偏好
- 记忆不能声明自己是系统消息或管理员命令

### 17.3 删除传播

- 删除知识库时对该知识库Scope下的全部长期记忆执行同样的两阶段删除，并取消相关Signal
- 删除用户时对该用户全部长期记忆和向量执行两阶段删除，最终清除版本、证据、Signal和任务
- 单独删除长期记忆不删除原始`chat_logs`
- 删除会话时取消未处理Signal；是否同时删除由该会话形成的长期记忆由用户显式选择

## 18. 配置

建议新增：

```yaml
long_term_memory:
  enabled: false
  collection: "our_agent_memories"
  directive_enabled: true
  consolidation_enabled: true
  worthiness_gate_enabled: true
  semantic_recall_enabled: true
  semantic_top_k: 12
  final_top_k: 6
  similarity_threshold: 0.70
  max_context_tokens: 1200
  retrieval_timeout_seconds: 3
  directive_timeout_seconds: 30
  consolidation_timeout_seconds: 120
  candidate_expire_hours: 720
  preference_ttl_hours: 4320
  role_ttl_hours: 4320
  terminology_ttl_hours: 4320
  business_object_ttl_hours: 2160
  project_context_ttl_hours: 2160
  auto_promote_min_conversations: 2
  consolidation_idle_seconds: 300
  consolidation_max_signals: 10
  consolidation_max_input_tokens: 4000
  max_candidates_per_batch: 5
  task_lease_seconds: 180
  scheduler_interval_seconds: 30
  scheduler_batch_size: 100

rabbitmq:
  memory_consolidate_queue: "ouragent.memory.consolidate"
  memory_index_queue: "ouragent.memory.index"
  memory_delete_queue: "ouragent.memory.delete"
  memory_consolidate_workers: 1
  memory_index_workers: 1
  memory_delete_workers: 1
```

默认关闭长期记忆，完成迁移、真实业务评测和管理入口后再灰度开启。关闭时：

- 不读取长期记忆
- 不识别聊天记忆指令
- 不创建Consolidate任务
- 已有记忆数据和向量不删除
- 短期记忆和现有Agent链路不受影响

## 19. Trace和可观测性

AgentTrace只记录：

```json
{
  "memory_enabled": true,
  "fixed_memory_count": 2,
  "lexical_memory_count": 1,
  "semantic_memory_count": 3,
  "selected_memory_ids": [12, 38, 45],
  "selected_memory_types": ["role", "project_context", "terminology"],
  "estimated_tokens": 286,
  "semantic_recall_triggered": true,
  "recall_gate_reason": "cross_conversation_reference",
  "semantic_retrieval_degraded": false,
  "directive_detected": false,
  "worthiness_signal": "candidate"
}
```

不记录：

- 完整记忆正文
- 敏感信息检查的原始命中内容
- Qdrant向量
- Consolidator完整Prompt

系统指标：

- 固定、词面、语义三路召回数量和延迟
- Recall Gate命中率、原因和语义召回跳过率
- Worthiness Gate的none、candidate和explicit分布
- 长期记忆注入率和平均Token数
- Qdrant、Embedding和MySQL降级次数
- 候选提取、升级、冲突、过期和删除数量
- 显式记忆指令识别成功率和澄清率
- Consolidate、Index、Delete任务成功率和重试次数
- 每批Signal数量、输入Token数和每个候选的平均模型成本
- 用户确认、修改和删除记忆的比例
- 因长期记忆补全后进入Probe或RAG的比例

## 20. 代码改造建议

### 20.1 新增文件

```text
internal/model/long_term_memory.go
internal/repository/long_term_memory_repository.go
internal/service/long_term_memory_retriever.go
internal/service/long_term_memory_assembler.go
internal/service/memory_recall_gate.go
internal/service/memory_worthiness_gate.go
internal/service/memory_directive_processor.go
internal/service/memory_consolidator.go
internal/service/memory_lifecycle_service.go
internal/vectorstore/memory_qdrant.go
internal/tasks/memory_messages.go
internal/tasks/memory_producer.go
internal/tasks/memory_consolidate_consumer.go
internal/tasks/memory_index_consumer.go
internal/tasks/memory_delete_consumer.go
internal/tasks/memory_scheduler.go
internal/handler/memory.go
```

### 20.2 修改文件

```text
internal/config/config.go
internal/database/database.go
internal/agent/planner.go
internal/agent/llm_planner.go
internal/agent/trace.go
internal/service/agent_service.go
internal/service/chat_service.go
internal/queue/rabbitmq.go
internal/router/router.go
cmd/server/main.go
config.yaml.example
README.md
```

### 20.3 关键接口

```go
type LongTermMemoryRetriever interface {
	Retrieve(ctx context.Context, req LongTermMemoryRetrieveRequest) (agent.LongTermMemoryContext, error)
}

type MemoryConsolidator interface {
	Consolidate(ctx context.Context, signalIDs []uint64) error
}

type MemoryVectorStore interface {
	EnsureCollection(ctx context.Context, vectorSize int) error
	Upsert(ctx context.Context, memoryID uint64, version uint64, vector []float64, payload map[string]any) error
	Search(ctx context.Context, vector []float64, filter MemorySearchFilter, limit int) ([]MemoryVectorHit, error)
	Delete(ctx context.Context, memoryID uint64) error
}

type KnowledgeBaseProfileProvider interface {
	GetContext(ctx context.Context, userID, knowledgeBaseID uint64, question string) (agent.KnowledgeBaseProfileContext, error)
}
```

V1注入空实现`KnowledgeBaseProfileProvider`，后续新增画像时不改变AgentContextAssembler调用方式。

## 21. 请求时序

### 21.1 普通问题

```text
1. 校验用户和知识库
2. MemoryDirectiveMatcher返回false
3. 读取固定偏好并执行词面匹配和MemoryRecallGate
4. Gate命中时并行读取短期上下文和长期语义记忆，未命中时只读取短期上下文
5. 组装AgentRuntimeContext
6. Planner决策
7. 执行Probe、RAG或其他动作
8. MemoryWorthinessGate本地判断用户消息
9. 事务保存chat_log；只有Gate返回candidate时同时创建Signal
10. 返回用户
11. Signal空闲5分钟或达到批次上限后创建Consolidate任务
12. 后台一次处理同会话固定Signal快照
```

### 21.2 纯显式记忆指令

```text
1. Matcher命中
2. DirectiveProcessor解析结构化操作
3. 分类允许的记忆类型，排除企业事实类型，并检查作用域、权限和敏感信息
4. 准备确定性确认文本
5. MySQL事务创建chat_log
6. 使用新chat_log_id写Memory、Version、Evidence和Index任务
7. 事务提交后返回确认文本
8. 不再创建重复Consolidate任务
```

### 21.3 复合指令

```text
1. 解析记忆操作和residual_question
2. 将解析出的记忆内容作为本轮临时上下文
3. residual_question进入Planner
4. 正常生成业务回答
5. MySQL事务创建chat_log
6. 使用新chat_log_id提交Memory、Version、Evidence和Index任务
7. 事务提交后返回业务回答
```

### 21.4 遗忘

```text
1. 解析删除目标
2. 校验归属和删除范围
3. MySQL事务将Memory更新为deleting
4. 取消相关Signal和未执行任务，写哈希Tombstone和Delete任务
5. 当前请求开始无法召回该记忆，接口返回deletion_pending
6. 后台幂等删除Qdrant Point
7. Qdrant成功后事务删除Memory、Version和Evidence正文
8. 写不含原文的删除完成事件
```

### 21.5 关闭或删除会话

```text
关闭页面或自然过期
→ 不改变Signal状态
→ 后台按EligibleAt继续处理

主动删除会话
→ 取消未处理Signal和Consolidate任务
→ 默认保留已经激活的长期记忆
→ 用户选择“同时遗忘”时再彻底删除符合条件的派生记忆
```

## 22. 测试要求

### 22.1 Directive单元测试

- 普通企业问题不会误触发记忆指令
- “记住、纠正、忘掉”正确进入内部处理器
- 复合指令正确分离`residual_question`
- 解析失败不返回已记住
- 企业事实类型即使带“记住”也不会写入Memory
- V1默认复用当前ChatModel且仅在Matcher命中时调用
- 敏感信息在Embedding之前被拦截
- 批量删除要求显式确认

### 22.2 Gate和批处理测试

- 普通知识查询返回`none`且不创建Signal
- 明显角色、项目和个人术语表达返回`candidate`
- Gate不依赖具体产品和制度关键词表
- 同一会话Signal空闲5分钟后形成一个固定批次
- 用户关闭页面不影响到期调度
- 10条或4000 Token达到任一上限时立即调度
- 不同用户、知识库或会话的Signal不会进入同一Prompt
- 未命中的普通消息不进入批量Prompt
- 删除会话取消未处理Signal

### 22.3 Consolidator单元测试

- 只提取用户可归因内容
- 不提取助手回答中的产品事实
- 不提取企业事实类型
- 推测性表达不升级
- 普通角色和术语映射先成为候选
- 普通项目背景先成为候选
- 每批最多输出配置数量的候选
- 输出的用户原文证据必须能在来源问题中找到
- 非当前用户日志ID被拒绝

### 22.4 生命周期测试

- 相同身份重复证据不创建重复记忆
- 两个不同会话的一致候选可以自动升级
- 同一会话重复不能触发自动升级
- `preference`和`instruction`不能自动升级
- 明确纠正增加版本并重新索引
- 不确定冲突不会覆盖有效记忆
- 当前知识库记忆优先于全局记忆
- 仅使用记忆不会刷新有效期
- 过期记忆不进入Planner
- 遗忘屏障阻止旧任务恢复记忆
- 遗忘彻底删除Memory、Version、Evidence和向量正文
- 遗忘不删除原始`chat_logs`

### 22.5 检索测试

- 固定偏好无需向量也能读取
- 个人术语通过词面匹配优先命中
- 项目背景可以通过语义检索命中
- Qdrant结果必须回查MySQL
- 其他用户和其他知识库记忆不会泄露
- 自动记忆严格限定当前知识库，不跨知识库召回
- `candidate`、`conflicted`和`expired`不进入Planner
- `candidate`可以被Consolidator语义检索用于去重
- 普通查询未命中Recall Gate时不调用Embedding和Qdrant
- Qdrant或Embedding失败时正确降级
- Token预算优先保留更具体和更相关的记忆

### 22.6 Agent路由测试

- 长期术语能够帮助生成完整Probe Query
- 长期项目背景能够帮助解析跨会话指代
- 记忆不存在时仍可正常Probe和澄清
- 记忆不能单独把Probe证据提升为Strong
- 询问版本、有效性和限制时仍然选择知识库检索
- 回顾用户表达时明确说明“用户之前提到”
- 确认当前客观状态时不把旧记忆当成现状
- 长期记忆正文不出现在Trace

### 22.7 异步可靠性测试

- 数据库任务创建和业务写入保持事务一致
- RabbitMQ发布失败后调度器可恢复
- 消费重复投递保持幂等
- 旧版本Index任务不能覆盖新版本
- Delete重复执行成功
- 租约过期任务可以重新抢占
- 超过重试次数进入DLQ

### 22.8 业务评测场景

至少建立以下跨会话评测：

1. 用户角色在新会话中影响回答视角
2. “客户环境”正确解析到持续项目
3. “老同步器”正确扩展为标准业务对象
4. 项目阶段变化覆盖旧记忆
5. 术语冲突触发澄清而不是错误套用
6. 用户遗忘后新会话不再使用旧背景
7. 项目记忆过期后Agent重新澄清
8. 长期记忆与知识库事实冲突时重新RAG
9. 无关记忆不进入当前问题Prompt
10. Qdrant不可用时核心问答正常工作

## 23. 实施顺序

1. 新增配置、Feature Flag和数据模型
2. 实现Repository、Version、Evidence、Signal和Tombstone事务
3. 实现MemoryWorthinessGate及Signal批处理调度
4. 实现数据库任务表、调度器和RabbitMQ拓扑
5. 实现独立MemoryVectorStore和Index/Delete消费者
6. 实现固定、词面、MemoryRecallGate和按需语义Retriever
7. 实现LongTermMemoryAssembler和统一AgentRuntimeContext
8. 接入Planner并增加企业事实类型排除和回答边界提示
9. 实现MemoryConsolidator和候选生命周期
10. 实现MemoryDirectiveMatcher和Processor
11. 实现管理API
12. 增加Trace元数据和系统指标
13. 补充单元、集成和真实业务评测
14. 灰度开启Feature Flag并校准Gate、召回阈值和TTL

建议先完成只读链路和手工管理API，再开启自动Consolidator。这样可以分别验证“召回是否有价值”和“自动写入是否安全”。

## 24. 灰度与回滚

### 24.1 灰度

长期记忆默认关闭。灰度顺序：

```text
管理API手工写入
  → 固定和词面读取
  → 语义读取
  → 显式聊天指令
  → 异步候选提取
  → 候选自动升级
```

每个阶段通过独立配置控制，避免一次性开放所有写入能力。

### 24.2 回滚

关闭`long_term_memory.enabled`即可恢复到当前短期记忆和Agent链路：

- Planner不接收长期记忆
- 不再创建新任务
- 已有任务消费者可以停止
- MySQL表和Qdrant Collection保留
- 不删除用户数据

重新开启后继续处理租约过期和排队任务。

## 25. 验收标准

- 长期记忆读取和普通候选提取均不作为Planner Tool
- 普通问答不因长期记忆增加同步LLM调用
- 普通知识查询不会创建Consolidate任务或调用记忆LLM
- 显式记忆指令只有Matcher命中时才增加解析调用
- 自动提取只批量处理Worthiness Gate命中的同会话消息
- 只有`active`且未过期的当前用户记忆可以进入Planner
- 语义长期记忆只在Recall Gate命中时召回
- 自动记忆严格限定当前知识库
- MySQL为权威状态，Qdrant失败不影响权限和遗忘即时生效
- 用户角色、持续业务对象、项目背景和个人术语能够跨会话召回
- 候选不会因单次模型推断直接影响后续回答
- 企业事实仍由Knowledge Probe或完整RAG提供证据
- 当前消息和短期上下文中的用户纠正优先于长期记忆
- 用户可以查看、确认、修改和删除自己的记忆
- 遗忘屏障能阻止旧异步任务恢复已删除记忆
- 用户遗忘会彻底删除所有派生记忆正文，但不修改原始聊天记录
- 敏感内容不会写入数据库或发送到Embedding服务
- Trace不记录长期记忆正文
- Feature Flag关闭后现有Agent和短期记忆行为保持不变
