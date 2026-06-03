# OurAgent v0.2 MVP 实现说明

本文档用于指导 OurAgent 第二版开发。v0.2 的目标不是直接进入复杂 Agent 阶段，而是在 v0.1 已经跑通 RAG 主链路的基础上，将 RAG 问答链路整体改造成 Eino Chain 编排，并补齐工程化 RAG 系统必须具备的可维护、可追踪、可调参、可流式输出能力。

## 1. 版本目标

v0.2 目标：

> 将 v0.1 的基础 RAG 链路升级为由 Eino 负责编排的工程化 RAG 系统。用户问题进入 RAG 后，检索、过滤、上下文构建、Prompt 渲染、模型调用和结果组装都应纳入 Eino Chain，为后续 Agent Tool Calling、Query Rewrite、Rerank 和多轮对话打基础。

v0.2 核心演进：

```text
v0.1:
用户问题 -> 手写 embedding -> Qdrant 检索 -> 手写 prompt 拼接 -> Chat 模型 -> 回答

v0.2:
用户问题 -> Eino RAG Chain
        -> Retriever Node
        -> Context Builder Node
        -> Prompt Template Node
        -> ChatModel Node
        -> Output Assembler Node
        -> 流式/非流式回答
```

## 2. v0.2 范围

### 2.1 必须实现

- 引入 Eino 框架
- 使用 Eino Chain 改造完整 RAG 问答链路
- RAG Service 不再手写检索、过滤、上下文拼接、Prompt 构造和模型调用流程
- 将 Retriever、Context Builder、Prompt Template、ChatModel、Output Assembler 组织为 Eino 节点
- 抽象 ChatModel 组件
- 抽象 Retriever 组件
- Prompt 构建模板化
- 保留现有 handler/service/repository 分层
- 保留 MySQL 作为业务数据存储
- 保留 Qdrant 作为向量数据库
- 支持删除单个文档
- 删除文档时同步删除 document_chunks 和 Qdrant 向量
- 支持重新索引文档
- 索引失败后支持手动重试
- 支持检索参数配置
- 支持 score_threshold
- 支持 max_context_tokens
- 支持 strict 模式
- 增强 chat_logs 中的检索 trace
- 支持 SSE 流式回答
- 保留普通非流式问答接口
- 支持用户对回答进行反馈
- 错误信息继续使用中文
- service 层错误继续使用 pkg/errors 包装

### 2.2 暂不实现

- 复杂 Agent Tool Calling
- ReAct Agent
- 多 Agent 协作
- Query Rewrite
- Rerank
- 混合检索
- 多轮对话记忆
- 文档版本管理
- 团队空间
- 复杂角色权限
- 自动化评测平台
- 前端管理后台

## 3. Eino 引入边界

v0.2 引入 Eino 的目标是让 RAG 问答链路由 Eino 负责编排，而不是只把 ChatModel 调用换成 Eino。Eino 不接管整个后端项目，但必须接管 RAG 链路内部的组件编排。

### 3.1 Eino 负责的内容

- ChatModel 抽象
- Retriever 抽象
- Prompt Template
- RAG Chain 编排
- 检索结果过滤节点
- 上下文构建节点
- 输出结果组装节点
- 流式生成节点
- 后续 Agent 能力预留

### 3.2 Eino 不负责的内容

- HTTP 路由
- 用户认证
- 权限校验
- MySQL 表结构
- repository 数据访问
- 文档上传接口
- 文件本地存储
- 业务错误码
- 统一响应结构

项目仍然保持：

```text
handler -> service -> repository
                  -> Eino RAG Chain
                  -> document indexer
```

handler/service/repository 仍然是后端业务边界。service 负责用户权限、知识库存在性校验、事务边界和日志落库；Eino 负责 RAG 链路内部的组件编排。

### 3.3 当前实现基线

当前 v0.2 已经完成 Eino ChatModel 接入，但 RAG 链路仍有较多流程由 `rag.Service` 手写：

- 检索参数解析
- 调用 Retriever
- score_threshold 过滤
- max_context_tokens 截断
- retrieval_trace 构造
- Prompt 构造
- 普通回答和流式回答的流程分支

后续改造要求：

> 将上述 RAG 链路内部步骤移动到 Eino Chain 中，`rag.Service` 只保留业务校验、调用 Chain、保存日志和错误转换。

## 4. 推荐模块调整

v0.2 建议新增或调整以下模块：

```text
internal/einoapp        Eino 组件初始化、RAG Chain 与节点适配
internal/rag            RAG 业务入口，负责校验、调用 Eino Chain、保存日志
internal/rag            Retriever 抽象与 Qdrant 实现，作为 Eino 节点被调用
internal/prompt         Prompt 模板管理，优先使用 Eino Prompt Template 能力
internal/stream         SSE 流式响应辅助
internal/feedback       用户反馈业务
```

现有模块保留：

```text
internal/handler
internal/service
internal/repository
internal/document
internal/vectorstore
internal/llm
internal/model
pkg/response
pkg/logger
```

如果 Eino 的模型适配稳定，可以逐步减少 `internal/llm/openai.go` 中手写 HTTP 逻辑。RAG 问答主链路必须优先使用 Eino ChatModel。

## 5. 功能设计

### 5.1 Eino RAG Chain

RAG 链路应被组织为 Eino Chain，而不是集中写在 `rag.Service` 中。

```text
service:
1. 校验用户是否能访问知识库
2. 校验知识库是否有已完成索引文档
3. 构造 RAG Chain 输入
4. 调用 Eino RAG Chain
5. 保存 chat_logs

Eino RAG Chain:
1. Normalize Query Node：清洗用户问题
2. Retriever Node：检索相关 chunk
3. Confidence Filter Node：根据 score_threshold 过滤低分 chunk
4. Context Builder Node：根据 max_context_tokens 组织上下文
5. Prompt Template Node：渲染 system/user messages
6. ChatModel Node：调用模型生成回答
7. Output Assembler Node：组装 answer、sources、retrieval_trace
```

v0.2 的重点是把 Retriever 到 Output Assembler 的 RAG 内部流程交给 Eino 编排。`rag.Service` 不应该再直接拼接 prompt 或直接管理每个 chunk 的过滤细节。

普通接口和流式接口应复用同一个 Eino RAG Chain：

```text
普通问答：Invoke/Generate -> 完整 answer
流式问答：Stream -> message chunk -> sources -> done
```

### 5.2 Retriever Node

定义 Retriever 的业务目标：

```text
输入：用户问题、用户 id、知识库 id、检索参数
输出：按相关性排序的 chunk 列表
```

Qdrant Retriever 可以继续使用当前业务抽象，但需要作为 Eino 节点接入 RAG Chain。

Qdrant Retriever Node 需要完成：

- 将 query 转为 embedding
- 调用 Qdrant 检索
- 按 user_id 和 knowledge_base_id 过滤
- 返回 chunk_id、document_id、score、payload
- 回查 MySQL 获取 chunk 原文和文档信息

Retriever 返回结果必须保持顺序，越相关的 chunk 越靠前。

### 5.3 Context Builder Node

Context Builder Node 负责把 Retriever 返回的 chunk 转成模型可用上下文，同时生成 sources 和 retrieval_trace。

需要完成：

- 根据 score_threshold 标记 chunk 是否可用
- 根据 max_context_tokens 控制上下文长度
- 记录每个 chunk 的 used、score、过滤原因
- 生成 sources
- 生成 context_text
- 生成 prompt_preview

该节点是 RAG 可解释性的核心，不应散落在 handler 或 service 中。

### 5.4 Prompt Template Node

v0.2 不再在业务代码中直接拼接大段 prompt 字符串，而是将 prompt 模板独立管理，并优先使用 Eino 的 Prompt Template / Message Template 能力。

基础模板要求：

```text
你是企业知识库问答助手。
请只根据给定的知识库上下文回答问题。
如果上下文中没有足够信息，请回答“根据当前知识库内容无法确认”。
不要编造上下文中不存在的信息。
回答应简洁、清晰，并尽量按条目组织。

知识库上下文：
{{context}}

用户问题：
{{question}}
```

后续可以为不同检索模式维护不同 prompt。

### 5.5 ChatModel Node

ChatModel Node 负责调用 Eino ChatModel。

要求：

- 非流式问答走 Eino Generate
- 流式问答走 Eino Stream
- 模型配置来自 `config.yaml` 或环境变量
- 支持 OpenAI-compatible API
- 当前主链路不再直接调用手写 `OpenAICompatibleChat.Chat`

### 5.6 Output Assembler Node

Output Assembler Node 负责把模型输出、sources 和 trace 组装为统一结果。

输出至少包含：

```text
answer
sources
retrieval_trace
prompt_preview
prompt_tokens
completion_tokens
```

如果模型返回空回答，应统一替换为：

```text
根据当前知识库内容无法确认。
```

### 5.7 检索参数配置

v0.2 支持以下参数：

```yaml
rag:
  top_k: 5
  score_threshold: 0.3
  max_context_tokens: 6000
  strict_mode: true
```

参数含义：

| 参数 | 说明 |
| --- | --- |
| top_k | 从向量数据库召回的 chunk 数量 |
| score_threshold | 最低相似度阈值 |
| max_context_tokens | 最多放入 prompt 的上下文 token 数 |
| strict_mode | 是否严格拒答低置信度问题 |

接口请求中可以允许覆盖部分参数，但必须有上限，避免用户传入过大的 top_k 或 context token。

### 5.8 Strict 模式

strict 模式开启时：

- 如果没有检索到 chunk，直接回答无法确认
- 如果最高 score 低于 score_threshold，直接回答无法确认
- 如果过滤后没有可用 chunk，直接回答无法确认

默认拒答文案：

```text
根据当前知识库内容无法确认。
```

strict 模式判断应尽量放在 Eino RAG Chain 中，而不是由 handler 或 service 分散判断。

### 5.9 文档删除

新增删除单个文档能力。

删除文档时必须同步处理：

```text
1. 校验文档是否属于当前用户
2. 删除 Qdrant 中该文档对应的向量点
3. 删除 document_chunks
4. 删除 documents 记录
5. 删除本地文件
```

如果 Qdrant 删除失败，不能只删除 MySQL 数据，否则会出现向量残留。

### 5.10 文档重新索引

新增手动重新索引接口。

重新索引流程：

```text
1. 校验文档是否属于当前用户
2. 将文档状态改为 pending
3. 删除旧 chunk
4. 删除旧 Qdrant 向量
5. 重新解析原文件
6. 重新切 chunk
7. 重新 embedding
8. 写入 Qdrant
9. 写入新的 chunk
10. 更新文档状态为 completed
```

如果失败，文档状态应更新为 failed，并记录中文错误原因。

### 5.11 SSE 流式回答

保留原接口：

```http
POST /api/v1/knowledge-bases/{id}/chat
```

新增流式接口：

```http
POST /api/v1/knowledge-bases/{id}/chat/stream
```

SSE 事件建议：

```text
event: message
data: {"content":"部分回答内容"}

event: sources
data: {"sources":[...]}

event: done
data: {"chat_log_id":1001}

event: error
data: {"message":"调用模型失败"}
```

流式接口也必须保存 chat_logs。

### 5.12 用户反馈

新增回答反馈能力。

用户可以对一次问答记录进行：

- 点赞
- 点踩
- 填写反馈原因

反馈用于后续分析低质量问答。

## 6. 数据模型调整

### 6.1 chat_logs 增强

建议扩展字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| retrieval_trace | json | 检索 trace |
| prompt_preview | text | 最终 prompt 摘要或上下文预览 |
| score_threshold | double | 本次使用的相似度阈值 |
| top_k | int | 本次使用的 topK |
| max_context_tokens | int | 本次最大上下文 token |
| strict_mode | bool | 是否严格模式 |

retrieval_trace 建议内容：

```json
{
  "query": "支付回调验签失败有哪些原因？",
  "hits": [
    {
      "chunk_id": 10,
      "document_id": 1,
      "score": 0.87,
      "used": true
    }
  ],
  "filtered_count": 1,
  "context_token_count": 1200
}
```

### 6.2 chat_feedbacks

新增问答反馈表。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | bigint | 主键 |
| chat_log_id | bigint | 问答日志 id |
| user_id | bigint | 用户 id |
| rating | varchar | like/dislike |
| reason | text | 反馈原因 |
| created_at | datetime | 创建时间 |
| updated_at | datetime | 更新时间 |

同一个用户对同一条 chat_log 只能保留一条反馈，可以更新覆盖。

## 7. API 设计

### 7.1 删除文档

```http
DELETE /api/v1/documents/{id}
Authorization: Bearer <token>
```

响应：

```json
{
  "code": 0,
  "message": "ok",
  "data": null
}
```

### 7.2 重新索引文档

```http
POST /api/v1/documents/{id}/reindex
Authorization: Bearer <token>
```

响应：

```json
{
  "document_id": 1,
  "status": "pending"
}
```

### 7.3 流式问答

```http
POST /api/v1/knowledge-bases/{id}/chat/stream
Authorization: Bearer <token>
Content-Type: application/json
Accept: text/event-stream
```

请求：

```json
{
  "question": "支付回调验签失败有哪些原因？",
  "top_k": 5,
  "score_threshold": 0.3,
  "strict_mode": true
}
```

### 7.4 提交反馈

```http
POST /api/v1/chat-logs/{id}/feedback
Authorization: Bearer <token>
```

请求：

```json
{
  "rating": "like",
  "reason": "回答准确，引用来源正确"
}
```

响应：

```json
{
  "id": 1,
  "chat_log_id": 1001,
  "rating": "like"
}
```

## 8. 错误处理要求

v0.2 继续沿用现有错误处理风格：

- handler 负责参数校验和错误响应
- service 返回 error
- service 层错误使用 sentinel error
- 业务错误使用 `pkgerrors.WithStack`
- 下游错误使用 `pkgerrors.WithMessage`
- handler 使用 `errors.Is` 判断业务错误
- 系统错误使用 zap 记录 `%+v`
- 返回给用户的错误信息使用中文

建议新增业务错误：

```go
ErrChatLogNotFound
ErrInvalidFeedback
ErrDocumentIndexing
ErrLowConfidence
```

## 9. 开发顺序

推荐按以下顺序实现：

```text
1. 保留当前 Eino ChatModel 接入
2. 定义 Eino RAG Chain 的输入输出结构
3. 将当前 QdrantRetriever 包装为 Retriever Node
4. 新增 Confidence Filter Node
5. 新增 Context Builder Node
6. 将 Prompt 构造迁移到 Eino Prompt Template Node
7. 将 ChatModel 调用迁移到 ChatModel Node
8. 新增 Output Assembler Node
9. 将普通问答接口改为调用 Eino RAG Chain Invoke
10. 将流式问答接口改为调用 Eino RAG Chain Stream
11. 从 rag.Service 中移除手写 RAG 内部编排逻辑
12. 保留 rag.Service 的业务校验、日志保存和错误转换职责
13. 确认 retrieval_trace、sources、prompt_preview 仍能正确落库
14. 保留文档删除、重建索引和反馈接口
15. 补充测试和示例请求
16. 更新 README 中已确认的 v0.2 能力
```

## 10. 验收标准

v0.2 完成后必须满足：

- 项目可以正常启动
- 原 v0.1 注册、登录、知识库、上传文档、问答链路不被破坏
- RAG 问答内部链路已通过 Eino Chain 编排
- Retriever、Context Builder、Prompt Template、ChatModel、Output Assembler 均作为 Eino 链路节点存在
- rag.Service 不再手写 chunk 过滤、上下文拼接、Prompt 构造和模型调用流程
- 可以通过普通接口获得完整回答
- 可以通过 SSE 接口获得流式回答
- 可以配置 top_k
- 可以配置 score_threshold
- 可以配置 max_context_tokens
- strict 模式下低置信度问题会拒答
- chat_logs 中能看到本次检索 trace
- 可以删除单个文档
- 删除文档后 MySQL chunk 和 Qdrant 向量同步删除
- 可以对失败或已有文档重新索引
- 可以对一次回答提交点赞或点踩反馈
- 用户 A 不能删除、重建索引或反馈用户 B 的数据
- 所有对外错误信息均为中文

## 11. 面试表达重点

v0.2 可以重点讲：

- 为什么从手写 RAG 链路演进到 Eino 编排
- Eino 在项目中承担什么职责
- 为什么不让 Eino 接管整个后端
- 为什么 service 只保留业务边界，而 RAG 内部链路交给 Eino Chain
- Retriever 抽象如何为混合检索和 rerank 预留空间
- Context Builder Node 如何控制上下文长度和生成 trace
- Prompt Template Node 如何统一提示词管理
- score_threshold 如何降低幻觉
- strict 模式如何控制无依据回答
- retrieval trace 如何帮助排查 RAG 效果问题
- SSE 流式回答在后端如何设计
- 删除文档时为什么必须同步删除向量数据
- 反馈数据如何反向驱动 RAG 优化

## 12. 完成定义

v0.2 的完成定义：

> 在保留 v0.1 主链路稳定性的前提下，项目完成 Eino 接入，并将 RAG 问答内部链路整体迁移到 Eino Chain。系统支持流式回答、文档删除与重建索引、检索参数控制、检索 trace 和用户反馈。此时项目从“基础 RAG Demo”升级为“由 Eino 编排的工程化 RAG 后端系统”。

