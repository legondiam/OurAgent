# OurAgent

OurAgent是一个面向企业知识库场景的Go后端服务，提供文档接入、异步索引、混合检索、重排序、可追踪回答、流式问答、Agentic RAG和外部知识源同步能力。

项目不是简单的文件上传问答Demo，而是围绕实际RAG链路构建：文档会被解析为结构化chunk，同时写入向量索引和关键词索引；问答时支持query rewrite、多路召回、RRF融合、重排序、上下文构建、来源引用和检索trace，便于排查回答依据。

## 功能特性

- 用户注册和JWT认证
- 知识库创建、查询和删除
- 文档上传、存储、异步索引、重建索引、列表查询和删除
- Markdown/TXT/PDF风格文档解析和切片
- 基于Qdrant的向量检索
- 基于Bluge BM25的关键词检索
- 基于RRF的向量和关键词混合召回
- query rewrite和多query召回
- DashScope兼容重排序
- 严格RAG回答、来源引用和检索trace
- 普通问答和SSE流式问答接口
- Agentic RAG问答接口，支持LLM Router、直接回答、会话上下文工具、知识库轻量探测、检索规划、澄清、拒答和联网补充
- 问答日志和反馈接口
- 知识库无答案时的联网搜索降级
- RabbitMQ异步文档索引和删除清理任务
- MinIO文档对象存储
- Notion OAuth以及Notion/飞书外部知识源增量同步
- 知识源定时调度、任务租约、失败重试和DLQ
- 远端缺失文档分阶段下线索引并异步清理本地数据

## 技术栈

- 语言：Go
- Web框架：Gin
- ORM/数据库：GORM + MySQL
- 向量数据库：Qdrant
- 关键词索引：Bluge
- 对象存储：MinIO
- 任务队列：RabbitMQ
- LLM编排：CloudWeGo Eino
- 模型接入：OpenAI兼容Chat和Embedding API
- 重排序/联网搜索：DashScope兼容API
- 配置管理：Viper + `.env` + `config.yaml`

## 架构概览

```text
Client
  |
  v
Gin API
  |
  +-- Auth / JWT
  +-- Knowledge base service
  +-- Document service
  |     |
  |     +-- MinIO object storage
  |     +-- RabbitMQ indexing task
  |
  +-- Source sync service
  |     |
  |     +-- Notion / Feishu connectors
  |     +-- RabbitMQ source sync task
  |     +-- Scheduled sync and lease recovery
  |     +-- Deindex / delete cleanup
  |
  +-- Chat service
  |     |
  |     +-- Query rewrite
  |     +-- Qdrant vector retrieval
  |     +-- Bluge BM25 retrieval
  |     +-- RRF hybrid fusion
  |     +-- Rerank
  |     +-- Context builder
  |     +-- Eino chat model
  |     +-- Source references and trace
  |
  +-- Agent service
        |
        +-- Agent Router Planner
        +-- DirectAnswerTool
        +-- ConversationContextTool
        +-- KnowledgeProbeTool
        +-- KnowledgeSearchTool
        +-- Post-RAG Planner
        +-- WebSearchTool
        +-- Agent trace
```

## 项目结构

```text
cmd/server              应用入口
internal/config         配置加载和环境变量绑定
internal/router         Gin路由注册
internal/handler        HTTP处理器
internal/service        业务服务
internal/repository     数据库访问层
internal/model          GORM模型
internal/agent          Agent trace、Planner和工具抽象
internal/document       文档解析、切片和索引
internal/rag            检索、重排序、trace和RAG链路
internal/search         Bluge关键词索引
internal/vectorstore    Qdrant客户端封装
internal/storage        MinIO客户端封装
internal/queue          RabbitMQ客户端
internal/tasks          文档索引和删除清理消费者
internal/source         外部知识源同步
internal/oauth          OAuth集成
internal/websearch      联网搜索降级
pkg/logger              Zap日志初始化
pkg/response            API响应封装
```

## 快速开始

### 1. 准备依赖服务

OurAgent运行时需要以下服务：

- MySQL
- Qdrant
- MinIO
- RabbitMQ

仓库中有意不跟踪`docker-compose.yml`。你可以使用自己的本地Docker Compose文件、已有本地服务或托管服务启动这些依赖。

### 2. 创建配置文件

复制示例配置并填写本地配置：

```powershell
Copy-Item config.yaml.example config.yaml
```

至少需要配置：

- `mysql.dsn`
- `qdrant.url`
- `minio.endpoint`
- `minio.access_key`
- `minio.secret_key`
- `rabbitmq.url`
- `llm.base_url`
- `llm.api_key`
- `llm.chat_model`
- `llm.embedding_model`
- `jwt.secret`

敏感配置也可以通过`.env`或环境变量提供。`config.yaml`已被Git忽略，应只保留在本地。

### 3. 安装Go依赖

```powershell
go mod download
```

### 4. 启动服务

```powershell
go run ./cmd/server
```

服务监听`server.port`配置的端口，默认是`8080`。

## API概览

基础路径：

```text
/api/v1
```

公开接口：

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/auth/register` | 用户注册 |
| `POST` | `/auth/login` | 用户登录并获取JWT |
| `GET` | `/oauth/notion/callback` | Notion OAuth回调 |

受保护接口需要JWT中间件：

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/knowledge-bases` | 创建知识库 |
| `GET` | `/knowledge-bases` | 查询知识库列表 |
| `DELETE` | `/knowledge-bases/:id` | 删除知识库 |
| `POST` | `/knowledge-bases/:id/documents` | 上传文档 |
| `GET` | `/knowledge-bases/:id/documents` | 查询文档列表 |
| `GET` | `/documents/:id` | 查询文档详情 |
| `DELETE` | `/documents/:id` | 删除文档 |
| `POST` | `/documents/:id/reindex` | 重建文档索引 |
| `POST` | `/knowledge-bases/:id/chat` | 知识库RAG问答 |
| `POST` | `/knowledge-bases/:id/chat/stream` | SSE流式RAG问答 |
| `POST` | `/knowledge-bases/:id/agent/chat` | Agentic RAG问答 |
| `GET` | `/chat-logs` | 查询问答日志 |
| `POST` | `/chat-logs/:id/feedback` | 提交问答反馈 |
| `POST` | `/knowledge-bases/:id/sources` | 创建外部知识源 |
| `GET` | `/knowledge-bases/:id/sources` | 查询外部知识源 |
| `POST` | `/knowledge-sources/:id/sync` | 触发知识源同步 |
| `GET` | `/knowledge-sources/:id/documents` | 查询同步的外部文档 |
| `GET` | `/oauth/notion/authorize` | 发起Notion授权 |

## RAG链路

OurAgent的普通问答流程基于Eino Chain实现：

```text
question
  -> query rewrite
  -> vector retrieval and BM25 retrieval
  -> RRF fusion
  -> rerank
  -> context building
  -> prompt rendering
  -> chat model
  -> answer, sources, and retrieval trace
```

开启strict mode时，系统只基于检索到的知识库上下文回答。如果没有可靠上下文，会返回兜底回答，而不是编造知识库外的信息。开启联网搜索降级后，普通问答接口可在知识库拒答时按配置使用外部搜索补充回答。

## Agentic RAG链路

`/agent/chat`在普通RAG外增加了一层Agent Router：

```text
question
  -> optional ConversationContextAssembler
  -> Pre-RAG / Context-Resolved LLM Planner
  -> conversation_answer / direct_answer / knowledge_probe / clarify / knowledge_search / web_search / reject
  -> optional ConversationAnswerTool
  -> optional DirectAnswerTool
  -> optional KnowledgeProbeTool
  -> optional Probe-Resolved LLM Planner
  -> KnowledgeSearchTool / WebSearchTool
  -> optional Post-RAG LLM Planner
  -> answer, sources, retrieval trace, agent trace
```

Agent Router基于Eino ChatModel实现LLM Planner，并使用原生function calling输出动作决策：function name对应Agent动作，真实工具执行仍由服务端统一调度。启用短期记忆后，已有会话会在Planner执行前自动装配结构化摘要和摘要后的原始问答，不再把上下文读取暴露为模型可选Tool。短会话使用完整原始历史，历史超过Token阈值后通过RabbitMQ异步压缩旧内容，并始终保留最近原始问答；摘要未完成或异常时按硬Token预算优先保留最新历史。

`direct_answer`仅用于寒暄、通用知识解释和写作辅助等不依赖企业知识库、会话回答和实时信息的问题。`conversation_answer`用于缩写、复述、翻译、格式转换或比较会话中的既有回答，必须携带来源`chat_log_id`，并继承原回答来源；版本变化、有效性、适用范围、例外等新的企业事实判断仍然重新查询知识库。

`knowledge_probe`用于问题看似通用、但可能包含企业产品、项目、型号、套餐、价格、规格等业务对象的场景。它复用现有召回器，只取少量候选的标题、路径、分数和预览，不生成最终答案；随后Probe-Resolved Planner会根据探测结果决定进入完整知识库检索、直接回答、澄清、联网搜索或拒答。

如果选择知识库检索，Planner会生成有限的`SearchPlan`，控制检索query、topK、query rewrite、hybrid和rerank开关。代码侧会校验Planner输出，只执行白名单动作；知识库检索低置信度时会进入Post-RAG Planner，由Agent结合检索摘要决定澄清、联网补充或拒答。`AgentTrace`会记录上下文版本、消息数量、Token数、降级状态、Planner决策、知识库轻量探测、知识库检索、联网搜索和最终回答模式，但不返回摘要正文。

连续问答场景下，`conversation_id`由后端托管。新会话请求可以不传`conversation_id`，服务端会生成新ID、写入问答日志并在响应中返回；同一聊天窗口后续追问时，前端继续携带上次返回的ID：

```json
{
  "conversation_id": "conv_xxx",
  "question": "为什么要部门审批？"
}
```

当请求携带`conversation_id`时，服务端会按`user_id`、`knowledge_base_id`和`conversation_id`校验归属。会话在最后一次成功问答7天后过期；同一会话同一时间只允许一个活跃请求，其他请求返回409，不同会话仍可并行处理。新会话记录和首轮`chat_log`在同一事务中创建，旧版本中只有`chat_logs`而没有`conversations`记录的会话不会回填。

## 配置说明

应用会加载`config.yaml`，并展开配置值中的环境变量。例如：

```yaml
llm:
  api_key: "${OPENAI_API_KEY}"

jwt:
  secret: "${JWT_SECRET}"
```

推荐的本地文件：

- `config.yaml`：本地运行配置，已被Git忽略
- `.env`：本地密钥，已被Git忽略
- `config.yaml.example`：可公开的示例配置，会被Git跟踪

文档索引任务使用数据库执行令牌和长租约恢复Consumer宕机后遗留的`processing`状态：

```yaml
rabbitmq:
  retry_delay_seconds: 30
  max_retries: 5
  index_lease_seconds: 1800
```

同一文档通过条件更新原子抢占。租约有效时，重复消息进入延迟队列等待且不增加失败次数；租约过期后，新Consumer使用新的执行令牌抢占任务，并按`document_id`清理旧切片、向量和关键词索引后完整重建。已经`completed`但尚未ACK的消息重新投递时会直接幂等完成，不会重复索引。

知识源同步支持低精度定时扫描和租约恢复：

```yaml
source_sync:
  scheduler_interval_seconds: 60
  lease_seconds: 1800
  schedule_batch_size: 100
  delete_after_missing_syncs: 2
```

同步任务通过数据库`sync_task_id`和条件状态更新防止同一知识源并发执行。普通失败进入RabbitMQ延迟重试队列，超过上限后保留`failed`状态并进入DLQ；`failed`不会被定时调度器自动重启，需要用户手动触发恢复。服务异常退出后，调度器会重新抢占租约过期的`queued`或`syncing`任务。

Agent短期记忆默认配置：

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

`short_term_enabled=false`时临时回退到旧`context_lookup`链路。摘要任务复用当前ChatModel和RabbitMQ延迟重试拓扑，任务失败不会阻断主问答。

远端文档第一次在完整列表中缺失时会进入`missing`状态并异步删除Qdrant、Bluge和父子切片，使其不能继续被RAG检索，同时保留MinIO原文和Document记录。连续两次完整同步缺失后才执行物理删除；如果文档重新出现，则重新拉取并恢复索引。

## 开发

运行测试：

```powershell
go test ./...
```

构建：

```powershell
go build ./cmd/server
```

发布前检查Git跟踪文件：

```powershell
git status --short
git ls-files config.yaml docker-compose.yml docs
```

该命令不应输出本地配置或Docker Compose文件；`docs/short-term-memory-implementation.md`属于明确加入白名单的实现说明，可以被跟踪。

## 仓库策略

本仓库只用于发布源代码和安全示例。

- 不提交`config.yaml`
- 不提交`.env`
- 不提交本地`docker-compose.yml`
- `docs/`默认忽略，仅跟踪明确加入白名单的实现说明
- 配置字段变化时同步更新`config.yaml.example`
