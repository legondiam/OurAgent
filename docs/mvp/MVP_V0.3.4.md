# OurAgent v0.3.4 MVP实现说明

本文档用于指导OurAgent v0.3.4开发。v0.3.1完成父子切片，v0.3.2完成QueryRewrite，v0.3.3完成Qdrant向量召回、Bluge BM25召回和RRF融合。v0.3.4继续优化召回后的排序质量，引入Rerank模型对候选结果进行精排。

## 1. 版本目标

v0.3.4目标：

> 在Hybrid Retrieval召回结果之后，调用Rerank模型对候选父chunk进行相关性精排，把最适合回答用户问题的上下文排到前面，降低“召回到了但排序不准”导致的回答质量问题。

核心链路：

```text
用户问题
-> QueryRewrite
-> Qdrant向量召回
-> Bluge BM25召回
-> RRF融合
-> Rerank模型精排
-> ContextBuilder构建Prompt上下文
-> ChatModel生成回答
```

## 2. 为什么需要Rerank

v0.3.3已经解决了“多路召回”的问题，但召回结果仍然可能存在排序不准：

```text
向量检索擅长语义相似，但可能把泛相关内容排前
BM25擅长关键词匹配，但可能把只包含关键词的弱相关内容排前
RRF能融合排名，但仍然只基于召回器排名，不真正理解query和chunk是否能回答问题
```

Rerank模型通常是Cross-Encoder类模型，它会同时读取：

```text
query
document
```

然后直接输出相关性分数。它比Embedding相似度更慢，但判断更细：

```text
Embedding: query向量和chunk向量是否相似
Rerank: 这个chunk是否真的能回答这个query
```

所以v0.3.4的定位是：

```text
Recall阶段尽量多找
Rerank阶段重新排序
ContextBuilder阶段只选最有用的父chunk
```

## 3. 模型选型建议

### 3.1 推荐首选：qwen3-vl-rerank

当前项目已经使用阿里百炼兼容接口调用Qwen Chat和Embedding模型，因此Rerank首选同平台的`qwen3-vl-rerank`。

推荐理由：

- 和当前阿里百炼API Key体系一致
- 不需要额外部署本地模型服务
- 支持文本、图片和视频多模态Rerank
- 接入成本低，适合作为MVP默认实现
- 可以直接用HTTP API封装，不影响现有Eino RAGChain
- 当前版本先只使用text文档格式，后续可扩展图片、PDF页面截图和视频帧精排

建议默认配置：

```yaml
rerank:
  enabled: true
  provider: "dashscope"
  model: "qwen3-vl-rerank"
  top_n: 8
  candidate_limit: 20
```

### 3.2 备选：BGE Reranker

如果后续希望私有化部署，推荐考虑：

```text
BAAI/bge-reranker-v2-m3
BAAI/bge-reranker-base
BAAI/bge-reranker-large
```

特点：

- 开源生态成熟
- 支持中文和英文
- 可以本地部署，避免外部API调用
- 需要额外Python推理服务或模型服务

暂不建议v0.3.4直接接入BGE，因为当前项目是Go后端，直接本地跑BGE会引入Python服务、GPU/CPU推理、服务治理和部署复杂度。可以作为后续“本地Rerank服务”方向。

### 3.3 备选：Cohere Rerank

Cohere Rerank模型能力较成熟，API也简单，但会引入新的模型供应商。

暂不建议作为默认方案，原因：

- 当前项目已经使用阿里百炼
- 多供应商配置和错误处理会增加MVP复杂度
- 国内网络和可用性需要单独评估

## 4. 实现范围

### 4.1 必须实现

- 新增Reranker抽象
- 新增DashScopeReranker实现
- Hybrid Retrieval之后调用Reranker
- Rerank输入使用用户原始问题
- Rerank候选使用召回后的父chunk内容
- Rerank结果按模型分数重新排序
- ContextBuilder继续按`score_threshold`和`max_context_tokens`筛选
- `retrieval_trace`记录rerank分数和排序变化
- 支持通过配置关闭Rerank
- 普通Chat和SSE Chat都支持Rerank链路

### 4.2 暂缓实现

- 本地BGE Reranker服务
- Cohere Rerank接入
- 多模型Rerank动态选择
- Rerank结果缓存
- Rerank失败重试
- 分段Rerank或超长父chunk压缩
- Rerank前后评测指标面板

## 5. Rerank位置

v0.3.3链路：

```text
queryRewriteNode
-> retrieveNode
-> contextBuilderNode
-> promptTemplateNode
-> chatModelNode
-> outputAssemblerNode
```

v0.3.4建议调整为：

```text
queryRewriteNode
-> retrieveNode
-> rerankNode
-> contextBuilderNode
-> promptTemplateNode
-> chatModelNode
-> outputAssemblerNode
```

流式链路同样加入`rerankNode`：

```text
queryRewriteNode
-> retrieveNode
-> rerankNode
-> contextBuilderNode
-> promptTemplateNode
-> streamChatModelNode
```

## 6. Rerank输入设计

### 6.1 Query选择

Rerank阶段建议使用用户原始问题：

```text
req.Question
```

原因：

- 最终回答要服务用户原始意图
- rewrite和expansion只是为了召回
- 如果用扩展问题Rerank，可能把上下文排序带向扩展方向

后续可以考虑：

```text
原问题 + 最佳rewrite合并成rerank query
```

但v0.3.4先只用原问题，保持可控。

### 6.2 Document选择

Rerank候选建议使用父chunk内容。

原因：

```text
child_chunk用于召回
parent_chunk用于LLM阅读
最终进入Prompt的是parent_chunk
```

所以Rerank应该判断：

```text
这个parent_chunk是否适合回答用户问题
```

候选文本建议格式：

```text
章节：{section_path}
内容：{parent_content}
```

如果`section_path`为空：

```text
内容：{parent_content}
```

### 6.3 候选数量

推荐：

```yaml
rerank:
  candidate_limit: 20
  top_n: 8
```

含义：

```text
candidate_limit: 从Hybrid Retrieval结果中最多取前多少个候选送入Rerank
top_n: Rerank后最多保留多少个候选进入ContextBuilder
```

为什么不要把全部候选都送Rerank：

- Rerank按query-document pair计费或耗时
- 父chunk文本较长，token成本更高
- 召回尾部候选质量较低，精排价值有限

## 7. 模块设计

### 7.1 新增Reranker接口

建议放在：

```text
internal/rag/reranker.go
```

接口：

```go
type Reranker interface {
	Rerank(ctx context.Context, req RerankRequest) (*RerankResult, error)
}
```

请求结构：

```go
type RerankRequest struct {
	Query     string
	Items     []RerankItem
	TopN      int
}
```

候选项：

```go
type RerankItem struct {
	Index         int
	ChildChunkID  uint64
	ParentChunkID uint64
	Text          string
}
```

返回：

```go
type RerankResult struct {
	Items []RerankResultItem
}
```

单项结果：

```go
type RerankResultItem struct {
	Index int
	Score float64
}
```

### 7.2 DashScopeReranker

建议放在：

```text
internal/rerank/dashscope.go
```

职责：

```text
组装DashScope rerank请求
发送HTTP请求
解析返回的index和relevance_score
返回统一RerankResult
```

请求格式概念：

```json
{
  "model": "qwen3-vl-rerank",
  "input": {
    "query": "用户原始问题",
    "documents": [
      {
        "text": "候选父chunk 1"
      },
      {
        "text": "候选父chunk 2"
      }
    ]
  },
  "parameters": {
    "return_documents": false,
    "top_n": 8
  }
}
```

### 7.3 FallbackReranker

当Rerank关闭或调用失败时，系统应退化为Hybrid Retrieval原排序。

建议实现：

```go
type FallbackReranker struct{}
```

行为：

```text
不改变原始顺序
不影响问答主链路
```

说明：v0.3.4中Rerank失败不应该导致问答失败，除非后续明确开启严格Rerank模式。

## 8. RAGChain调整

`RAGChain`新增依赖：

```go
reranker Reranker
```

新增节点：

```go
func (c *RAGChain) rerankNode(ctx context.Context, state *chainState) (*chainState, error)
```

节点职责：

```text
1. 检查rerank是否启用
2. 从state.results取前candidate_limit个候选
3. 构造父chunk文本
4. 调用Reranker
5. 按rerank分数重排state.results
6. 记录rerank trace
7. 失败时记录错误并保留原排序
```

## 9. 排序策略

v0.3.4建议采用：

```text
最终排序 = Rerank模型排序
```

RRF分数不再作为最终排序依据，但保留在trace中。

如果Rerank返回分数相同或缺失：

```text
保留原Hybrid排序
```

排序字段建议：

```text
rerank_score DESC
原始rank ASC
```

## 10. 类型调整

`RetrievedChunk`建议新增：

```go
RerankScore float64
RerankRank  int
BeforeRerankRank int
```

`TraceHit`建议新增：

```go
RerankScore float64 `json:"rerank_score,omitempty"`
RerankRank int `json:"rerank_rank,omitempty"`
BeforeRerankRank int `json:"before_rerank_rank,omitempty"`
```

`RetrievalTrace`建议新增：

```go
RerankEnabled bool `json:"rerank_enabled"`
RerankModel string `json:"rerank_model,omitempty"`
RerankError string `json:"rerank_error,omitempty"`
RerankCandidateCount int `json:"rerank_candidate_count"`
```

## 11. 配置设计

建议新增：

```yaml
rerank:
  enabled: true
  provider: "dashscope"
  base_url: "https://dashscope.aliyuncs.com/api/v1"
  api_key: "${OPENAI_API_KEY}"
  model: "qwen3-vl-rerank"
  candidate_limit: 20
  top_n: 8
  timeout_seconds: 30
```

说明：

| 字段 | 说明 |
| --- | --- |
| enabled | 是否启用Rerank |
| provider | 当前实现为dashscope |
| base_url | DashScope API基础地址 |
| api_key | API Key |
| model | Rerank模型名 |
| candidate_limit | 送入Rerank的候选数量 |
| top_n | Rerank后保留数量 |
| timeout_seconds | 调用超时时间 |

## 12. API调整

Chat请求可以新增可选字段：

```json
{
  "question": "notify_url没收到怎么办？",
  "query_rewrite": true,
  "hybrid": true,
  "rerank": true
}
```

说明：

- `rerank`不传时使用配置默认值
- `rerank=false`时跳过Rerank，直接使用Hybrid排序
- 普通Chat和SSE Chat保持一致

## 13. Trace示例

```json
{
  "query": "notify_url没收到怎么办？",
  "rewrite_enabled": true,
  "hybrid_enabled": true,
  "rerank_enabled": true,
  "rerank_model": "qwen3-vl-rerank",
  "rerank_candidate_count": 12,
  "hits": [
    {
      "chunk_id": 12,
      "parent_chunk_id": 3,
      "recall_sources": ["vector", "bm25"],
      "vector_score": 0.81,
      "bm25_score": 4.32,
      "rrf_score": 0.032,
      "before_rerank_rank": 3,
      "rerank_rank": 1,
      "rerank_score": 0.91,
      "used": true
    }
  ]
}
```

## 14. 开发顺序

建议按以下顺序实现：

```text
1. 新增Rerank配置
2. 新增Reranker接口和类型
3. 实现DashScopeReranker
4. 实现FallbackReranker
5. 扩展RetrievedChunk和TraceHit
6. RAGChain新增reranker依赖
7. EinoChain插入rerankNode
8. rerankNode构造父chunk候选文本
9. rerankNode按模型分数重排state.results
10. Chat请求支持rerank开关
11. 普通Chat链路联调
12. SSE链路联调
13. 验证Rerank失败时退化为Hybrid排序
```

## 15. 验收标准

v0.3.4完成后需要满足：

- 可以通过配置开启或关闭Rerank
- Chat请求可以临时关闭Rerank
- Rerank输入使用用户原始问题
- Rerank候选使用父chunk内容
- Rerank后结果顺序会影响ContextBuilder取上下文顺序
- retrieval_trace记录rerank_score
- retrieval_trace记录rerank前后排名
- retrieval_trace记录rerank_model和rerank_error
- Rerank失败时问答链路不失败
- 普通Chat和SSE Chat都能正常工作
- QueryRewrite、Hybrid Retrieval、RRF、父子切片能力不被破坏

## 16. 面试表达重点

v0.3.4可以重点讲：

- 为什么召回后还需要Rerank
- Embedding召回、BM25召回、RRF融合和Rerank分别解决什么问题
- 为什么Rerank使用原始问题而不是扩展query
- 为什么Rerank候选使用父chunk而不是子chunk
- 为什么Rerank失败要退化为Hybrid排序
- 如何通过trace观察Rerank前后排名变化
- 如何控制Rerank候选数量，平衡效果、成本和延迟

