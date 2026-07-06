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
- Notion OAuth和外部知识源同步

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
  -> Pre-RAG LLM Planner
  -> direct_answer / context_lookup / knowledge_probe / clarify / knowledge_search / web_search / reject
  -> optional DirectAnswerTool
  -> optional ConversationContextTool
  -> optional Context-Resolved LLM Planner
  -> optional KnowledgeProbeTool
  -> optional Probe-Resolved LLM Planner
  -> KnowledgeSearchTool / WebSearchTool
  -> optional Post-RAG LLM Planner
  -> answer, sources, retrieval trace, agent trace
```

Agent Router基于Eino ChatModel实现LLM Planner。Planner会在检索前判断用户问题是否可以直接回答、是否需要读取会话上下文、轻量探测知识库、澄清、查询知识库、直接联网或拒答。`direct_answer`仅用于寒暄、通用知识解释、写作辅助和格式转换等不依赖企业知识库和实时信息的问题，不返回知识库来源；如果选择`context_lookup`，服务端会读取同一用户、同一知识库、同一`conversation_id`下最近几轮问答，再让Planner基于历史进行二次决策。二次决策阶段不允许再次选择`context_lookup`，避免循环。

`knowledge_probe`用于问题看似通用、但可能包含企业产品、项目、型号、套餐、价格、规格等业务对象的场景。它复用现有召回器，只取少量候选的标题、路径、分数和预览，不生成最终答案；随后Probe-Resolved Planner会根据探测结果决定进入完整知识库检索、直接回答、澄清、联网搜索或拒答。

如果选择知识库检索，Planner会生成有限的`SearchPlan`，控制检索query、topK、query rewrite、hybrid和rerank开关。代码侧会校验Planner输出，只执行白名单动作；知识库检索低置信度时会进入Post-RAG Planner，由Agent结合检索摘要决定澄清、联网补充或拒答。`AgentTrace`会记录Planner决策、直接回答、上下文工具调用、知识库轻量探测、知识库检索、联网搜索和最终回答模式。

连续问答场景下，客户端可以传入`conversation_id`：

```json
{
  "conversation_id": "conversation-uuid",
  "question": "为什么要部门审批？"
}
```

当`conversation_id`为空时，系统按单轮问题处理，不暴露`context_lookup`工具，也不会默认读取用户最近日志，避免新话题被旧上下文污染。

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

如果本地配置、Docker Compose文件和内部文档没有被跟踪，最后一条命令应该没有输出。

## 仓库策略

本仓库只用于发布源代码和安全示例。

- 不提交`config.yaml`
- 不提交`.env`
- 不提交本地`docker-compose.yml`
- 不提交`docs/`下的内部规划文档
- 配置字段变化时同步更新`config.yaml.example`
