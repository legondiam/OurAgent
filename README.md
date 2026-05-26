# OurAgent

企业知识库 RAG Agent 后端项目需求文档。

> 维护约定：README 只记录已经确定的项目需求、设计取舍和迭代规划；临时讨论、备选想法和未确认内容不写入本文档。

## 1. 项目定位

OurAgent 是一个面向企业内部知识场景的 RAG Agent 平台。

项目不是简单的“上传 PDF 后问答”Demo，而是一个更接近真实后端工程的 AI 应用系统，重点展示：

- 文档接入与异步索引能力
- 向量检索、混合检索与重排序能力
- 基于知识库的可追溯问答能力
- Agent 工具调用与任务编排能力
- 权限隔离、安全控制、日志追踪与效果评测能力
- Go 后端工程化设计能力

一句话介绍：

> OurAgent 是一个支持企业文档接入、语义检索、智能问答、引用溯源、Agent 工具调用和 RAG 效果评测的 Go 后端系统。

## 2. 项目背景

企业内部通常存在大量分散知识，例如技术文档、接口文档、产品 FAQ、运维手册、客服工单、项目 README、数据库表结构说明等。

传统知识管理方式存在几个问题：

- 文档分散，搜索成本高
- 关键词搜索无法理解语义
- 新人需要反复询问有经验的同事
- 文档更新后难以及时被使用
- 大模型直接回答容易幻觉，缺少来源依据
- 企业内部知识有权限边界，不能简单交给通用模型处理

RAG 通过在生成答案前检索外部知识，可以缓解大模型知识过时、领域知识不足和回答不可追溯的问题。Agentic RAG 进一步让模型能够根据问题动态决定是否检索、如何检索、是否调用工具以及是否进行二次查询。

## 3. 目标用户

### 3.1 普通用户

- 后端开发者
- 测试工程师
- 运维人员
- 客服/售后人员
- 产品经理
- 新入职员工

### 3.2 管理用户

- 知识库管理员
- 系统管理员
- 团队负责人

## 4. 核心业务场景

用户可以基于企业知识库进行提问，例如：

- 用户登录失败一般有哪些原因？
- 订单状态流转规则是什么？
- 某个接口的鉴权方式是什么？
- 支付模块常见异常应该如何排查？
- 根据已有文档，总结一份接口接入指南。
- 对比两个版本的接口文档有什么变化？
- 根据运维手册生成一份故障排查步骤。

系统需要基于用户有权限访问的知识内容回答，并返回引用来源。

## 5. 核心设计关注点

本项目需要重点解决 RAG Agent 在真实企业知识场景中的核心问题。

### 5.1 RAG 基础链路

需要实现：

- 文档如何解析
- 文本如何清洗
- chunk 如何切分
- embedding 如何生成
- 向量如何入库
- 用户 query 如何检索
- 检索结果如何进入 prompt
- LLM 如何生成带引用的答案

### 5.2 Chunk 策略

需要重点考虑：

- 固定长度切分和结构化切分的区别
- chunk size 和 overlap 的取舍
- 如何保留标题、章节路径等上下文
- 如何增强 Markdown 标题层级、代码块、表格、链接等结构化解析
- 如何避免答案跨 chunk 导致上下文丢失

### 5.3 检索质量

需要覆盖：

- 向量检索
- 关键词检索
- 混合检索
- 语义搜索
- 相似文档/相似 chunk 发现
- metadata 过滤
- topK 配置
- 相似度阈值
- rerank 重排序
- 召回率和准确率的平衡

### 5.4 幻觉与可追溯

需要解决：

- 答案必须基于检索内容
- 无依据时明确回答无法确认
- 返回引用来源
- 记录检索片段和最终答案，便于审计

### 5.5 Agentic RAG

需要能区分普通 RAG 和 Agentic RAG：

- 普通 RAG：先检索，再生成
- Agentic RAG：由 Agent 判断是否检索、检索什么、调用哪个工具、是否需要二次检索
- 后续可以通过 MCP 将数据库、日志、监控、工单、代码仓库等外部系统封装为 Agent 可调用工具
- 静态知识优先进入 RAG 知识库，实时数据、精确查询和外部系统状态更适合通过工具按需获取

### 5.6 工程化能力

需要体现：

- 异步任务
- 失败重试
- 限流
- 缓存
- 日志
- trace
- 成本统计
- Docker Compose 本地启动

### 5.7 权限与安全

需要重点设计：

- 用户只能检索有权限的文档
- tenant_id 隔离
- permission_scope 过滤
- prompt injection 防护
- Agent 工具白名单
- 外部 URL/domain 白名单
- 敏感信息过滤

### 5.8 评测体系

需要能够回答：

- 如何证明 RAG 效果好？
- 如何衡量检索质量？
- 如何衡量答案是否可靠？
- 如何持续优化 prompt、chunk、检索和 rerank？

## 6. 功能需求

### 6.1 用户与权限模块

功能：

- 用户注册与登录
- 用户信息管理
- 团队/空间管理
- 角色权限管理
- 文档级权限控制
- 查询时按权限过滤可检索内容

角色建议：

- Admin：系统管理员
- KnowledgeBaseOwner：知识库负责人
- Editor：文档维护者
- Viewer：普通查询用户

业务亮点：

- 权限过滤应发生在检索阶段，而不是生成答案后再过滤。
- 每个 chunk 的 metadata 中都需要带上租户、知识库和权限信息。

### 6.2 知识库管理模块

功能：

- 创建知识库
- 编辑知识库
- 删除知识库
- 查看知识库文档数量
- 查看知识库索引状态
- 配置知识库默认检索策略

知识库属性：

- knowledge_base_id
- name
- description
- owner_id
- tenant_id
- visibility
- default_retrieval_mode
- created_at
- updated_at

### 6.3 文档管理模块

一期支持：

- Markdown
- TXT
- PDF
- HTML
- API 文档文本

二期扩展：

- Git 仓库文档同步
- 数据库 schema 导入
- 飞书、Notion、Confluence 等企业知识源同步
- S3 / OSS / 企业网盘文件同步
- 客服工单、运维告警、API 文档平台等业务系统接入
- 外部数据源增量同步和删除同步

功能：

- 上传文档
- 删除文档
- 更新文档
- 查看解析状态
- 查看索引状态
- 文档标签与分类
- 文档版本管理

文档属性：

- document_id
- knowledge_base_id
- tenant_id
- title
- source_type
- source_url
- file_type
- file_size
- status
- version
- created_by
- created_at
- updated_at

### 6.4 文档处理与索引模块

功能：

- 文档解析
- 文本清洗
- Markdown 结构化解析
- chunk 切分
- metadata 生成
- embedding 向量化
- 向量库写入
- 索引任务状态追踪
- 失败任务重试
- 支持重建索引
- 支持发现相似文档和相似 chunk

chunk metadata 建议：

- chunk_id
- document_id
- knowledge_base_id
- tenant_id
- owner_id
- permission_scope
- source_url
- title
- section_path
- heading_level
- block_type
- chunk_index
- token_count
- created_at
- version

业务亮点：

- 文档索引应走异步任务，避免上传接口阻塞。
- 文档更新后需要支持增量索引或重建索引。
- chunk 中保存章节路径，提升回答可读性和引用质量。
- Markdown 文档应尽量保留标题层级、代码块、表格和链接等结构信息，提升检索和引用质量。
- 系统后续可基于向量相似度发现相关文档和相关 chunk，让 RAG 不只用于问答，也能用于知识发现。

### 6.5 检索模块

检索能力：

- 向量检索
- 关键词检索
- 混合检索
- 语义搜索
- 相似文档推荐
- 相似 chunk 推荐
- metadata 过滤
- 权限过滤
- rerank 重排序
- topK 配置
- 相似度阈值配置

检索模式：

- Fast：向量检索，低延迟，适合普通问答
- Accurate：混合检索 + rerank，适合准确性要求更高的问题
- Strict：只允许基于高置信度文档回答，否则返回无法确认

业务亮点：

- 支持不同检索策略，体现系统可调优能力。
- 记录检索 trace，便于分析为什么答错。
- 检索能力应可以独立于问答使用，既服务 LLM，也服务用户主动搜索和知识发现。

### 6.6 RAG 问答模块

功能：

- 用户提问
- 检索相关文档
- 构造 prompt
- 调用 LLM
- 流式返回答案
- 返回引用来源
- 保存对话记录
- 支持多轮上下文

回答要求：

- 答案必须基于检索内容
- 不得编造知识库中没有的信息
- 如果没有足够依据，需要明确说明无法确认
- 每个关键结论尽量关联引用来源
- 返回 source 列表，便于用户追溯

推荐响应结构：

```json
{
  "answer": "根据知识库内容，...",
  "sources": [
    {
      "document_id": "doc_001",
      "title": "支付模块运维手册",
      "section_path": "故障排查/支付超时",
      "chunk_id": "chunk_001",
      "score": 0.89
    }
  ],
  "trace_id": "trace_001"
}
```

业务亮点：

- 使用 SSE 支持流式输出。
- 返回引用来源，降低大模型黑盒感。
- 通过严格 prompt 和置信度阈值控制幻觉。

### 6.7 Agent 模块

Agent 模块是本项目的高级亮点。

普通 RAG 流程：

```text
用户问题 -> 检索 -> 生成答案
```

Agentic RAG 流程：

```text
用户问题 -> 意图判断 -> 选择工具 -> 检索/查询/总结/二次检索 -> 生成答案
```

计划支持的工具：

- search_knowledge_base：检索知识库
- summarize_document：总结指定文档
- compare_documents：比较多个文档
- query_database_schema：查询数据库结构说明
- query_service_logs：查询服务日志
- query_metrics：查询监控指标
- query_tickets：查询客服或故障工单
- search_code_repository：检索代码仓库
- create_troubleshooting_steps：生成排查步骤
- ask_clarification：问题不明确时反问用户

Agent 决策场景：

- 事实问题：直接检索知识库
- 总结问题：检索多个 chunk 后生成总结
- 对比问题：分别检索多个主题后组织答案
- 模糊问题：先追问用户
- 低置信度问题：改写 query 后二次检索

Agent 安全限制：

- 工具白名单
- 最大调用步数
- 单次工具超时
- 总请求超时
- 禁止访问未授权知识库
- 所有工具调用写入审计日志

后续 MCP 方向：

- 将外部系统能力标准化为可调用工具
- 优先考虑数据库 schema、日志、监控、工单和代码仓库等后端常见系统
- Agent 调用 MCP 工具时必须经过权限校验、参数校验、超时控制和审计记录
- MCP 用于补充实时数据、精确查询和外部系统状态，不替代静态文档 RAG

### 6.8 评测与反馈模块

功能：

- 用户点赞/点踩
- 标记答案是否有用
- 标记引用是否准确
- 保存问题、召回文档、最终答案
- 管理员查看低质量问答
- 支持测试集批量评测

评测指标：

- 检索命中率
- 引用准确率
- 答案满意度
- 平均响应时间
- token 成本
- 无答案正确拒答率

业务亮点：

- RAG 系统不是能跑就行，需要持续评估。
- 通过日志和反馈反向优化 chunk、prompt、检索策略和 rerank。

### 6.9 可观测性模块

功能：

- 请求日志
- 检索日志
- Agent 工具调用日志
- LLM 调用耗时
- token 消耗统计
- 错误率统计
- 慢请求分析

一次问答建议记录：

```text
question
rewritten_query
retrieved_chunks
rerank_score
prompt_tokens
completion_tokens
model_name
latency
tool_calls
final_answer
sources
```

业务亮点：

- 没有 trace 的 RAG 系统很难调优。
- 可观测性是区分真实工程项目和简单 Demo 的关键。

### 6.10 安全模块

功能：

- prompt injection 检测
- 工具调用白名单
- URL/domain 白名单
- 文档权限隔离
- 外部数据源访问权限控制
- MCP 工具调用权限控制
- 敏感词/敏感信息过滤
- 请求频率限制
- 用户操作审计

安全原则：

- 用户上传文档内容不能覆盖系统指令
- Agent 不能随意访问外部地址
- 检索必须带 tenant_id 和 permission_scope
- 高风险工具必须有明确参数校验

### 6.11 管理后台模块

功能：

- 知识库列表
- 文档列表
- 索引任务状态
- 问答日志
- 用户反馈
- 模型配置
- 检索参数配置
- 成本统计

## 7. 技术选型建议

后端：

- Go
- Gin / Echo / Fiber
- GORM / sqlc

存储：

- MySQL / PostgreSQL：业务数据
- Redis：缓存、限流、任务状态
- Qdrant / Milvus / pgvector：向量检索

AI 能力：

- Embedding Provider 抽象
- LLM Provider 抽象
- 支持 OpenAI、Claude、本地模型等不同供应商

工程能力：

- 异步任务队列
- SSE 流式响应
- OpenTelemetry trace
- Docker Compose 本地部署
- 配置化检索策略
- 模块化 Agent Tool

## 8. MVP 范围

第一版建议控制范围，优先做出闭环。

MVP 功能：

- 用户登录
- 创建知识库
- 上传 Markdown / TXT / PDF
- 异步解析和索引
- 向量检索
- RAG 问答
- SSE 流式输出
- 引用来源
- 问答日志
- 简单反馈
- 基础管理后台 API

MVP 暂不做：

- 多数据源同步
- 复杂前端管理后台
- 多 Agent 协作
- 自动化评测平台
- 复杂权限模型

## 9. 进阶版本规划

第二阶段：

- 混合检索
- rerank
- Query rewrite
- 多轮对话
- 独立语义搜索接口
- 相似文档/相似 chunk 推荐
- Markdown 结构化解析增强
- 文档级权限过滤
- Agent 工具调用
- prompt injection 防护

第三阶段：

- RAG 评测集
- 批量评测
- trace 可视化
- 多知识库路由
- Git 仓库文档同步
- 数据库 schema 问答
- 多数据源接入
- MCP 工具接入
- 成本统计看板

第四阶段：

- 多租户 SaaS 化
- 企业 SSO
- 审计报表
- 多模型动态路由
- 知识库自动更新
- 私有化部署方案

## 10. 项目亮点

项目需要体现以下亮点：

- 企业知识统一接入，包括文档、接口说明、运维手册、FAQ、工单和数据库结构说明。
- 后续支持多数据源接入，将 Git 仓库、数据库 schema、企业文档平台、工单和监控日志等知识统一纳入系统。
- 文档索引异步化，支持任务状态、失败重试和重建索引。
- 检索链路可配置，支持向量检索、关键词检索、混合检索和 rerank。
- 支持知识发现能力，通过语义搜索、相似文档和相似 chunk 推荐帮助用户主动发现相关知识。
- 回答结果带引用来源，支持追溯到文档、章节或原始来源。
- Agent 工具调用受控，具备工具白名单、超时、最大步数和审计日志。
- 后续可通过 MCP 标准化接入外部工具，让 Agent 在权限控制下按需查询实时数据和外部系统状态。
- 系统内置可观测性，记录 query、retrieval、rerank、prompt、tool call、token 和 latency。
- 系统具备评测闭环，通过用户反馈和测试集持续优化检索与回答质量。

## 11. 需求基线

- 项目语言以 Go 为主，突出后端工程能力。
- 项目方向是企业知识库 RAG Agent，不做泛聊天机器人。
- 第一阶段先完成 RAG 闭环，Agent 能力放在进阶阶段。
- README 只维护当前确定的项目需求，不记录临时讨论过程。

## 12. 本地开发

v0.1 本地开发约定：

- MySQL 使用本机已有服务。
- Qdrant 使用 Docker Compose 启动。
- 配置通过 Viper 读取 `config.yaml` 和 `.env`。

启动 Qdrant：

```bash
docker compose up -d
```

启动后端：

```bash
go run ./cmd/server
```
