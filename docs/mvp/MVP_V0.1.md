# OurAgent v0.1 MVP 实现说明

本文档用于指导 OurAgent 第一版开发。v0.1 的目标不是做完整平台，而是跑通企业知识库 RAG 主链路。

## 1. 版本目标

v0.1 目标：

> 用户可以创建知识库，上传文档，系统异步解析并建立向量索引，用户基于知识库提问，系统检索相关 chunk，调用大模型生成答案，并返回引用来源。

核心闭环：

```text
用户登录
  -> 创建知识库
  -> 上传文档
  -> 文档解析
  -> 文本切片
  -> 生成 embedding
  -> 写入向量库
  -> 用户提问
  -> 检索相关 chunk
  -> 组装 prompt
  -> 调用 LLM
  -> 返回答案和引用来源
  -> 保存问答日志
```

## 2. v0.1 范围

### 2.1 必须实现

- 用户注册
- 用户登录
- JWT 鉴权
- 创建知识库
- 查询知识库列表
- 删除知识库
- 上传文档
- 查询文档列表
- 查询文档详情和索引状态
- 支持 Markdown / TXT / PDF 文档
- 异步文档解析和索引
- 文本 chunk 切分
- 调用 embedding 模型生成向量
- 向量入库
- 基于指定知识库提问
- 检索 topK 相关 chunk
- 调用 chat 模型生成答案
- 返回引用来源
- 保存问答日志

### 2.2 暂不实现

- Agent 多步工具调用
- 多租户
- 团队空间
- 复杂角色权限
- 文档级细粒度权限
- 混合检索
- rerank
- Query rewrite
- 多轮对话记忆
- 自动化评测
- 可视化管理后台
- GraphRAG
- 代码仓库问答 Agent

## 3. 技术约束

后端语言：

- Go

Web 框架：

- Gin

业务数据库：

- MySQL

向量数据库：

- Qdrant

缓存/任务队列：

- v0.1 可以先不引入 Redis
- 文档索引任务可以先使用 Go goroutine + 数据库状态字段实现
- 后续版本再替换为 Redis Stream / Asynq / RabbitMQ 等正式任务队列

模型接口：

- 使用 OpenAI-compatible API
- Embedding 和 Chat 模型都通过 provider 抽象调用
- 具体模型通过配置文件或环境变量配置

配置方式：

- 使用 Viper 读取配置
- `.env`
- `config.yaml`

部署方式：

- 本地开发优先
- MySQL 使用本机已有服务
- 提供 `docker-compose.yml` 启动 Qdrant

## 4. 模块划分

建议第一版按以下模块拆分：

```text
cmd/server               程序入口
internal/config          配置加载
internal/database        MySQL 连接
internal/vectorstore     Qdrant 客户端
internal/middleware      鉴权、中间件
internal/model           数据模型
internal/repository      数据访问层
internal/service         业务逻辑
internal/handler         HTTP handler
internal/rag             RAG 核心链路
internal/document        文档解析、切片、索引
internal/llm             LLM 和 embedding provider
pkg/response             统一响应结构
pkg/errors               错误定义
```

## 5. 数据模型

### 5.1 users

用户表。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | bigint | 主键 |
| username | varchar | 用户名，唯一 |
| email | varchar | 邮箱，可为空 |
| password_hash | varchar | 密码哈希 |
| created_at | datetime | 创建时间 |
| updated_at | datetime | 更新时间 |

### 5.2 knowledge_bases

知识库表。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | bigint | 主键 |
| user_id | bigint | 所属用户 |
| name | varchar | 知识库名称 |
| description | text | 描述 |
| created_at | datetime | 创建时间 |
| updated_at | datetime | 更新时间 |

### 5.3 documents

文档表。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | bigint | 主键 |
| knowledge_base_id | bigint | 所属知识库 |
| user_id | bigint | 上传用户 |
| filename | varchar | 原始文件名 |
| file_type | varchar | 文件类型，md/txt/pdf |
| file_path | varchar | 本地存储路径 |
| status | varchar | pending/processing/completed/failed |
| error_message | text | 失败原因 |
| chunk_count | int | chunk 数量 |
| created_at | datetime | 创建时间 |
| updated_at | datetime | 更新时间 |

### 5.4 document_chunks

文档 chunk 表。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | bigint | 主键 |
| document_id | bigint | 所属文档 |
| knowledge_base_id | bigint | 所属知识库 |
| user_id | bigint | 所属用户 |
| chunk_index | int | chunk 序号 |
| content | text | chunk 文本 |
| token_count | int | 估算 token 数 |
| vector_id | varchar | Qdrant point id |
| created_at | datetime | 创建时间 |

### 5.5 chat_logs

问答日志表。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | bigint | 主键 |
| knowledge_base_id | bigint | 知识库 id |
| user_id | bigint | 用户 id |
| question | text | 用户问题 |
| answer | text | 模型回答 |
| retrieved_chunks | json | 检索到的 chunk 信息 |
| model_name | varchar | 使用的 chat 模型 |
| prompt_tokens | int | prompt token 数，可为空 |
| completion_tokens | int | completion token 数，可为空 |
| latency_ms | int | 总耗时 |
| created_at | datetime | 创建时间 |

## 6. API 设计

统一响应格式：

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

### 6.1 用户认证

#### 注册

```http
POST /api/v1/auth/register
```

请求：

```json
{
  "username": "demo",
  "email": "demo@example.com",
  "password": "123456"
}
```

响应：

```json
{
  "id": 1,
  "username": "demo"
}
```

#### 登录

```http
POST /api/v1/auth/login
```

请求：

```json
{
  "username": "demo",
  "password": "123456"
}
```

响应：

```json
{
  "token": "jwt-token",
  "user": {
    "id": 1,
    "username": "demo"
  }
}
```

### 6.2 知识库

#### 创建知识库

```http
POST /api/v1/knowledge-bases
Authorization: Bearer <token>
```

请求：

```json
{
  "name": "支付系统知识库",
  "description": "支付接口、故障排查和业务规则"
}
```

#### 查询知识库列表

```http
GET /api/v1/knowledge-bases
Authorization: Bearer <token>
```

#### 删除知识库

```http
DELETE /api/v1/knowledge-bases/{id}
Authorization: Bearer <token>
```

### 6.3 文档

#### 上传文档

```http
POST /api/v1/knowledge-bases/{id}/documents
Authorization: Bearer <token>
Content-Type: multipart/form-data
```

字段：

```text
file: 文档文件
```

响应：

```json
{
  "document_id": 1,
  "status": "pending"
}
```

上传成功后立即返回，后台开始索引。

#### 查询文档列表

```http
GET /api/v1/knowledge-bases/{id}/documents
Authorization: Bearer <token>
```

#### 查询文档详情

```http
GET /api/v1/documents/{id}
Authorization: Bearer <token>
```

响应需要包含：

```json
{
  "id": 1,
  "filename": "pay.md",
  "file_type": "md",
  "status": "completed",
  "chunk_count": 12,
  "error_message": ""
}
```

### 6.4 RAG 问答

#### 提问

```http
POST /api/v1/knowledge-bases/{id}/chat
Authorization: Bearer <token>
```

请求：

```json
{
  "question": "支付回调验签失败有哪些原因？",
  "top_k": 5
}
```

响应：

```json
{
  "answer": "根据知识库内容，支付回调验签失败通常包括以下原因：...",
  "sources": [
    {
      "document_id": 1,
      "document_name": "pay.md",
      "chunk_id": 10,
      "chunk_index": 3,
      "score": 0.87,
      "content_preview": "验签失败通常需要检查密钥、参数排序、编码方式..."
    }
  ],
  "chat_log_id": 1001
}
```

#### 查询问答日志

```http
GET /api/v1/chat-logs
Authorization: Bearer <token>
```

## 7. 文档索引流程

文档上传后，系统需要执行：

```text
1. 保存上传文件到本地 storage/documents
2. 插入 documents 记录，status = pending
3. 启动异步索引任务
4. 将 documents.status 更新为 processing
5. 根据文件类型解析文本
6. 清洗文本
7. 按 chunk_size 和 chunk_overlap 切分文本
8. 为每个 chunk 调用 embedding 模型
9. 将向量写入 Qdrant
10. 将 chunk 元数据写入 document_chunks
11. 更新 documents.status = completed
12. 如果失败，更新 documents.status = failed，并记录 error_message
```

### 7.1 文档解析要求

v0.1 支持：

- `.txt`：直接读取文本
- `.md`：直接读取文本，保留标题
- `.pdf`：提取文本内容即可，不要求表格、图片、OCR

PDF 解析第一版只要求可用，不追求复杂版式理解。

### 7.2 Chunk 策略

默认配置：

```yaml
rag:
  chunk_size: 1000
  chunk_overlap: 200
  top_k: 5
```

切分规则：

- 优先按段落切分
- 超过 chunk_size 时再按长度切分
- 相邻 chunk 保留 chunk_overlap
- chunk 中尽量保留 Markdown 标题上下文

## 8. RAG 问答流程

用户提问后，系统需要执行：

```text
1. 校验用户是否有权限访问知识库
2. 将 question 转成 embedding
3. 在 Qdrant 中按 knowledge_base_id 过滤检索 topK chunk
4. 根据 vector_id 回查 document_chunks 和 documents
5. 组装上下文 context
6. 组装 prompt
7. 调用 Chat LLM
8. 返回 answer 和 sources
9. 写入 chat_logs
```

### 8.1 Prompt 要求

第一版 prompt 需要满足：

```text
你是企业知识库问答助手。
请只根据给定的知识库上下文回答问题。
如果上下文中没有足够信息，请回答“根据当前知识库内容无法确认”。
不要编造上下文中不存在的信息。
回答后尽量用简洁条目组织。

知识库上下文：
{{context}}

用户问题：
{{question}}
```

## 9. 配置项

建议配置：

```yaml
server:
  port: 8080

mysql:
  dsn: "root:password@tcp(127.0.0.1:3306)/our_agent?charset=utf8mb4&parseTime=True&loc=Local"

qdrant:
  url: "http://localhost:6333"
  collection: "our_agent_chunks"

llm:
  base_url: "https://api.openai.com/v1"
  api_key: "${OPENAI_API_KEY}"
  chat_model: "gpt-4o-mini"
  embedding_model: "text-embedding-3-small"

rag:
  chunk_size: 1000
  chunk_overlap: 200
  top_k: 5
```

环境变量：

```text
OPENAI_API_KEY
JWT_SECRET
```

## 10. 向量库要求

Qdrant collection：

```text
collection: our_agent_chunks
vector_size: 由 embedding 模型决定
distance: cosine
```

每个 point payload 至少包含：

```json
{
  "chunk_id": 1,
  "document_id": 1,
  "knowledge_base_id": 1,
  "user_id": 1,
  "chunk_index": 3,
  "document_name": "pay.md"
}
```

检索时必须带：

```text
knowledge_base_id
user_id
```

避免用户检索到不属于自己的文档。

## 11. 错误处理

需要覆盖：

- 用户名重复
- 登录密码错误
- JWT 为空或无效
- 知识库不存在
- 无权访问知识库
- 文件为空
- 文件类型不支持
- 文档解析失败
- embedding 调用失败
- 向量库写入失败
- LLM 调用失败
- 知识库没有已完成索引的文档
- 检索不到相关 chunk

当检索不到相关 chunk 时，返回：

```json
{
  "answer": "根据当前知识库内容无法确认。",
  "sources": []
}
```

## 12. 验收标准

v0.1 完成后必须满足：

- 可以启动后端服务
- 可以连接 MySQL
- 可以连接 Qdrant
- 可以注册用户
- 可以登录并获取 JWT
- 未登录访问业务接口会被拒绝
- 可以创建知识库
- 可以上传 Markdown 文档
- 文档上传后状态从 pending/processing 变为 completed
- 可以在数据库看到文档 chunk
- 可以在 Qdrant 看到向量数据
- 可以基于知识库提问
- 回答内容来自检索到的文档
- 响应中包含 sources
- 可以查看问答日志
- 用户 A 不能访问用户 B 的知识库和文档

## 13. 推荐开发顺序

按以下顺序实现：

```text
1. 项目目录结构
2. 配置加载
3. MySQL 连接
4. 数据模型和迁移
5. 用户注册登录
6. JWT 鉴权中间件
7. 知识库 CRUD
8. 文件上传和 documents 表
9. 文档解析
10. chunk 切分
11. embedding provider
12. Qdrant 写入
13. 异步索引任务
14. RAG 检索
15. chat provider
16. 问答接口
17. sources 返回
18. chat_logs
19. docker-compose
20. README 补充启动方式
```

## 14. v0.1 演示脚本

演示流程：

```text
1. 启动 MySQL、Qdrant 和后端服务
2. 注册用户 demo
3. 登录获取 token
4. 创建“支付系统知识库”
5. 上传 pay.md
6. 等待索引完成
7. 提问：“支付回调验签失败有哪些原因？”
8. 系统返回答案和引用来源
9. 查看问答日志
```

演示文档内容可以包含：

```markdown
# 支付回调验签

支付回调验签失败通常需要检查以下内容：

1. 回调参数是否完整。
2. 参数排序是否与签名规则一致。
3. 商户密钥是否配置正确。
4. 字符编码是否统一为 UTF-8。
5. 回调报文是否被网关或代理修改。
```

## 15. 完成定义

v0.1 的完成定义：

> 使用一份 Markdown 或 PDF 文档创建知识库后，用户可以稳定提问并得到带引用来源的回答；系统能记录文档索引状态、chunk、向量数据和问答日志。

只要这个闭环稳定，v0.1 就算完成。
