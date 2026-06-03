# OurAgent v0.3.2 MVP实现说明

本文档用于指导OurAgent v0.3.2开发。v0.3.1已经完成父子切割，当前检索链路是“子chunk向量检索，父chunk进入Prompt”。v0.3.2继续优化查询侧，重点是QueryRewrite和MultiQuery检索，让用户的自然问题更容易命中知识库内容。

## 1. 版本目标

v0.3.2目标：

> 在不改变文档索引结构的前提下，引入查询改写能力。系统先对用户问题进行语义标准化、指代补全和查询扩展，再使用多个查询共同检索Qdrant，最后合并、去重、排序检索结果，提高召回率和可解释性。

核心变化：

```text
v0.3.1:
用户问题 -> 向量化 -> Qdrant检索 -> 回查父chunk -> Prompt

v0.3.2:
用户问题 -> QueryRewrite -> 多个检索query -> Qdrant多路检索 -> 合并去重排序 -> 回查父chunk -> Prompt
```

## 2. 实现范围

### 2.1 必须实现

- 新增QueryRewriter抽象
- 支持将用户问题改写为更适合检索的书面化问题
- 支持解决简单指代不明问题
- 支持将用户问题扩展为多个检索问题
- 检索时同时使用原问题和改写后的问题
- 多路检索结果按child_chunk_id合并去重
- 同一个child被多个query命中时保留最高分
- retrieval_trace记录query改写结果和每个query的命中情况
- 支持通过配置控制是否启用QueryRewrite
- 保持v0.3.1父子切割、流式回答、sources、trace能力不被破坏

### 2.2 暂缓实现

- HyDE默认启用
- 后退提问默认启用
- 基于历史多轮对话的复杂指代消解
- QueryRewrite结果缓存
- QueryRewrite失败后的重试策略
- Rerank模型重排
- 根据问题类型自动选择不同rewrite策略

说明：HyDE和后退提问是有价值的，但它们会额外调用模型，并且可能引入噪声。v0.3.2先把查询改写框架、多查询检索、结果合并和trace做好，后续再逐步打开更激进的策略。

## 3. 核心概念

### 3.1 QueryRewrite

QueryRewrite是把用户原始问题转换成更适合向量检索的问题。

示例：

```text
原问题:
这个失败了怎么办？

改写后:
支付回调验签失败时应该如何处理？
```

它主要解决：

- 用户口语化表达
- 问题太短
- 指代不清
- 查询词和文档用词不一致
- 缺少业务上下文

### 3.2 MultiQuery

MultiQuery是把一个用户问题扩展成多个检索query，并分别检索。

示例：

```text
用户问题:
支付回调验签失败怎么办？

检索query:
1. 支付回调验签失败怎么办？
2. 支付回调验签失败的处理流程是什么？
3. 回调签名校验失败是否会更新订单状态？
```

优势：

- 提高召回率
- 覆盖同义表达
- 降低单次向量表达不准确的影响

风险：

- query过多会增加Embedding和Qdrant调用次数
- 扩展问题质量差时会带入噪声
- 需要合并去重和分数控制

## 4. 四种策略取舍

### 4.1 简单查询改写

v0.3.2必须实现。

作用：

```text
口语问题 -> 书面化问题
省略表达 -> 补全业务对象
模糊指代 -> 尽量改成明确问题
```

特点：

- 成本低
- 易解释
- 最适合作为第一版QueryRewrite

### 4.2 多问题扩展

v0.3.2必须实现。

作用：

```text
一个原始问题 -> 多个检索问题
```

默认策略：

```text
保留原问题
生成1到3个扩展问题
最多使用rewrite_max_queries个query参与检索
```

### 4.3 HyDE

v0.3.2暂缓默认启用。

HyDE的含义是先让模型根据问题生成一段“假设答案”，再用这段假设答案去做向量检索。

示例：

```text
用户问题:
支付回调验签失败怎么办？

HyDE生成:
当支付回调验签失败时，系统通常不会更新订单状态，需要记录失败原因并返回失败响应...

检索时使用这段假设答案向量化
```

优势：

- 对抽象问题更友好
- 可能比短问题更容易命中长文档

风险：

- 假设答案可能编造
- 容易把检索方向带偏
- 额外消耗一次模型调用

### 4.4 后退提问

v0.3.2暂缓默认启用。

后退提问是先把具体问题抽象成背景问题，再辅助检索。

示例：

```text
原问题:
支付回调验签失败怎么办？

后退问题:
支付回调的处理流程和安全校验机制是什么？
```

优势：

- 适合文档中没有直接答案但有背景说明的情况
- 能帮助模型拿到更完整的上下文

风险：

- 背景问题太宽，可能召回大量无关内容
- 需要更强的结果筛选和上下文控制

## 5. 新增配置

建议在`rag`配置下新增：

```yaml
rag:
  query_rewrite_enabled: true
  query_rewrite_max_queries: 3
  query_rewrite_include_original: true
  query_rewrite_model_temperature: 0.1
  query_rewrite_hyde_enabled: false
  query_rewrite_step_back_enabled: false
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| query_rewrite_enabled | 是否启用查询改写 |
| query_rewrite_max_queries | 最多参与检索的query数量 |
| query_rewrite_include_original | 是否保留用户原问题参与检索 |
| query_rewrite_model_temperature | 改写模型温度 |
| query_rewrite_hyde_enabled | 是否启用HyDE |
| query_rewrite_step_back_enabled | 是否启用后退提问 |

默认值：

```text
query_rewrite_enabled = true
query_rewrite_max_queries = 3
query_rewrite_include_original = true
query_rewrite_model_temperature = 0.1
query_rewrite_hyde_enabled = false
query_rewrite_step_back_enabled = false
```

## 6. 模块设计

### 6.1 新增rag/query_rewriter.go

建议新增接口：

```go
type QueryRewriter interface {
	Rewrite(ctx context.Context, req RewriteRequest) (*RewriteResult, error)
}
```

请求结构：

```go
type RewriteRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
	MaxQueries      int
	IncludeOriginal bool
}
```

返回结构：

```go
type RewriteResult struct {
	OriginalQuery string
	Queries       []RewrittenQuery
}
```

单个query结构：

```go
type RewrittenQuery struct {
	Query  string
	Type   string
	Reason string
}
```

Type建议取值：

```text
original
rewrite
expansion
hyde
step_back
```

### 6.2 新增LLMQueryRewriter

LLMQueryRewriter使用Eino ChatModel完成问题改写。

Prompt要求：

```text
你是RAG检索查询优化器。请把用户问题改写成更适合知识库检索的问题。
要求：
1. 保留原始意图
2. 不要编造业务事实
3. 如果指代不清，只做合理补全
4. 输出JSON
5. 最多输出N个query
```

返回JSON示例：

```json
{
  "queries": [
    {
      "query": "支付回调验签失败时应该如何处理？",
      "type": "rewrite",
      "reason": "将口语问题改写为明确业务问题"
    },
    {
      "query": "支付回调签名校验失败是否会更新订单状态？",
      "type": "expansion",
      "reason": "扩展订单状态相关检索方向"
    }
  ]
}
```

### 6.3 FallbackQueryRewriter

当QueryRewrite关闭或模型调用失败时，使用FallbackQueryRewriter。

行为：

```text
只返回原始问题
不影响主问答链路
```

注意：模型改写失败不应该导致问答失败，除非后续明确打开严格改写模式。

## 7. RAGChain流程调整

当前v0.3.1链路：

```text
retrieveNode
-> contextBuilderNode
-> promptTemplateNode
-> chatModelNode
-> outputAssemblerNode
```

v0.3.2调整为：

```text
queryRewriteNode
-> retrieveNode
-> contextBuilderNode
-> promptTemplateNode
-> chatModelNode
-> outputAssemblerNode
```

流式链路同样增加`queryRewriteNode`：

```text
queryRewriteNode
-> retrieveNode
-> contextBuilderNode
-> promptTemplateNode
-> streamChatModelNode
```

### 7.1 queryRewriteNode

职责：

```text
接收用户原始问题
调用QueryRewriter生成多个检索query
写入chainState
写入retrieval_trace
```

如果rewrite失败：

```text
记录rewrite错误
退化为只使用原问题检索
主链路继续执行
```

### 7.2 retrieveNode

retrieveNode从单query检索改成多query检索。

流程：

```text
1. 遍历rewritten_queries
2. 每个query调用Retriever.Retrieve
3. 合并所有RetrievedChunk
4. 按child_chunk_id去重
5. 同一个child保留最高score
6. 记录该chunk由哪些query命中
7. 按最终score降序返回
```

## 8. Retriever调整

`Retriever.Retrieve`可以保持当前接口不变：

```go
Retrieve(ctx context.Context, req RetrieveRequest) ([]RetrievedChunk, error)
```

多query编排放在RAGChain中，不放进QdrantRetriever。

原因：

- QdrantRetriever只负责“一个query如何检索”
- RAGChain负责“多个query如何编排”
- 后续HybridSearch、Rerank、HyDE都更容易组合

## 9. 结果合并策略

多query命中结果需要合并。

### 9.1 child级别去重

主去重键：

```text
matched_child_chunk_id
```

即：

```go
RetrievedChunk.MatchedChunk.ID
```

同一个child被多个query命中时：

```text
保留最高score
记录matched_queries
```

### 9.2 parent级别去重

ContextBuilder继续沿用v0.3.1逻辑：

```text
多个child命中同一个parent时，只让parent进入一次上下文
```

区别是v0.3.2需要在trace里能看出：

```text
这个parent是由哪些query间接命中的
```

### 9.3 分数策略

v0.3.2先使用简单策略：

```text
final_score = max(score)
```

暂缓实现：

```text
final_score = max(score) + matched_query_count_boost
```

原因：加权策略需要更多测试，否则可能让低质量扩展query影响排序。

## 10. Trace增强

`RetrievalTrace`建议新增：

```go
RewriteEnabled bool `json:"rewrite_enabled"`
RewrittenQueries []TraceQuery `json:"rewritten_queries"`
RewriteError string `json:"rewrite_error,omitempty"`
```

`TraceQuery`：

```go
type TraceQuery struct {
	Query  string `json:"query"`
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}
```

`TraceHit`建议新增：

```go
MatchedQueries []string `json:"matched_queries"`
```

示例：

```json
{
  "query": "这个失败了怎么办？",
  "rewrite_enabled": true,
  "rewritten_queries": [
    {
      "query": "这个失败了怎么办？",
      "type": "original"
    },
    {
      "query": "支付回调验签失败时应该如何处理？",
      "type": "rewrite",
      "reason": "补全业务对象和失败类型"
    }
  ],
  "hits": [
    {
      "chunk_id": 12,
      "parent_chunk_id": 3,
      "score": 0.82,
      "matched_queries": [
        "支付回调验签失败时应该如何处理？"
      ],
      "used": true
    }
  ]
}
```

## 11. API调整

### 11.1 Chat请求

请求可以新增可选字段：

```json
{
  "knowledge_base_id": 1,
  "question": "这个失败了怎么办？",
  "top_k": 5,
  "score_threshold": 0.3,
  "max_context_tokens": 6000,
  "strict_mode": true,
  "query_rewrite": true
}
```

说明：

- `query_rewrite`不传时使用配置默认值
- 设置为`false`时只使用原问题检索
- SSE接口和普通Chat接口保持一致

### 11.2 Chat响应

普通响应结构不强制变化。

`sources`仍然返回：

```json
{
  "document_id": 1,
  "document_name": "pay.md",
  "section_path": "支付模块/回调处理",
  "parent_chunk_id": 3,
  "chunk_id": 12,
  "score": 0.82,
  "content_preview": "支付回调验签失败时..."
}
```

详细rewrite信息进入chat_logs的retrieval_trace。

## 12. ChatLog调整

当前chat_logs已经保存retrieval_trace。

v0.3.2不强制新增表字段，只需要让retrieval_trace JSON包含：

```text
是否启用rewrite
rewrite生成了哪些query
每个query类型
rewrite失败原因
命中chunk由哪些query召回
```

这样用户之后查看历史问答时，可以解释：

```text
原问题是什么
系统改写成了什么
最后是哪个query命中了答案
```

## 13. Prompt要求

QueryRewrite使用独立Prompt，不和最终回答Prompt混在一起。

改写Prompt约束：

```text
只改写查询，不回答问题
不引入知识库外的具体结论
不生成过多查询
输出必须是JSON
```

最终回答Prompt保持v0.3.1逻辑：

```text
只根据检索到的父chunk上下文回答
无法确认时返回兜底回答
```

## 14. 开发顺序

建议按以下顺序实现：

```text
1. 在Config中新增QueryRewrite配置
2. 在ChatRequest中新增query_rewrite可选字段
3. 在rag包新增QueryRewriter接口和类型
4. 实现FallbackQueryRewriter
5. 实现LLMQueryRewriter
6. 扩展Request和chainState保存rewritten_queries
7. 在RAGChain中新增queryRewriteNode
8. 修改retrieveNode支持多query检索
9. 实现child级别合并去重
10. 增强RetrievalTrace和TraceHit
11. 普通Chat链路联调
12. SSE链路联调
13. 验证rewrite失败时可以退化为原问题检索
```

## 15. 验收标准

v0.3.2完成后需要满足：

- 可以通过配置开启或关闭QueryRewrite
- Chat请求可以临时关闭QueryRewrite
- 用户原问题会默认参与检索
- LLM可以生成1到3个改写或扩展query
- 多个query都会参与Qdrant检索
- 多路检索结果会按child_chunk_id去重
- 同一个child被多个query命中时保留最高score
- ContextBuilder仍然使用parent_chunk进入Prompt
- 同一个parent仍然不会重复进入Prompt
- retrieval_trace能看到改写query列表
- retrieval_trace能看到命中chunk来自哪些query
- QueryRewrite失败时主问答链路不失败
- 普通Chat和SSE Chat都能正常工作
- v0.3.1父子切割、sources、feedback、delete、reindex能力不被破坏

## 16. 面试表达重点

v0.3.2可以重点讲：

- 为什么RAG不只优化索引，也要优化查询
- 用户问题和文档表达不一致时，QueryRewrite如何提高召回
- 为什么保留原问题参与检索，避免改写误伤
- MultiQuery如何覆盖同义表达和不同检索方向
- 多路检索结果如何合并、去重和保留最高分
- QueryRewrite为什么要进入trace，方便排查检索链路
- 为什么HyDE和后退提问有价值，但不应该第一版默认启用

