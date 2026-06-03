# OurAgent v0.3.3 MVP实现说明

本文档用于指导OurAgent v0.3.3开发。v0.3.1已经完成父子切片，v0.3.2已经完成QueryRewrite和多query向量检索。v0.3.3继续优化召回层，引入Hybrid Retrieval多路召回：一路使用Qdrant向量检索，一路使用Bluge BM25关键词检索，最后通过RRF融合排序。

## 1. 版本目标

v0.3.3目标：

> 在现有父子切片和QueryRewrite基础上，引入向量召回与关键词召回的混合检索能力。向量检索负责语义召回，BM25负责字段名、错误码、接口名、专有名词等关键词召回，两路结果通过RRF融合，提高知识库问答的整体召回稳定性。

核心链路：

```text
用户问题
-> QueryRewrite生成原问题、改写问题和扩展问题
-> Qdrant向量召回使用全部query
-> Bluge BM25召回只使用原始用户问题
-> RRF融合两路召回结果
-> 回查MySQL获取child和parent
-> parent去重后进入Prompt
```

## 2. 技术选型

BM25关键词召回使用Bluge实现。

选择Bluge的原因：

- 纯Go全文检索库，不需要额外部署Elasticsearch或OpenSearch
- 支持BM25相关性评分
- 支持文本字段、数值字段、MatchQuery等检索能力
- 可以作为项目内部检索模块维护，方便和现有Go工程集成
- 后续可以扩展短语检索、高亮、字段权重和更复杂BooleanQuery

v0.3.3暂不使用MySQL FULLTEXT。

原因：

- MySQL全文检索和业务库耦合较强
- 中文分词能力依赖MySQL配置
- 后续字段权重、索引重建和检索策略扩展不如独立检索库灵活

## 3. 实现范围

### 3.1 必须实现

- 引入Bluge依赖
- 新增Bluge索引目录配置
- 在文档索引阶段同步写入Bluge索引
- 在文档删除和重建索引时同步删除Bluge索引
- 新增BM25Retriever
- 新增HybridRetriever
- Qdrant向量召回使用v0.3.2生成的全部query
- Bluge BM25召回只使用原始用户问题
- 使用RRF合并向量召回和BM25召回结果
- 合并结果仍然按child_chunk_id去重
- ContextBuilder继续按parent_chunk_id去重
- retrieval_trace记录召回通道和RRF分数
- 普通Chat和SSE Chat都支持Hybrid Retrieval

### 3.2 暂缓实现

- Elasticsearch或OpenSearch
- Bluge中文分词深度优化
- BM25多query召回
- 字段权重调优
- PhraseQuery短语召回
- Highlight高亮
- Rerank模型重排
- 根据问题类型动态选择召回通道
- Bluge索引增量修复任务

## 4. 召回策略

### 4.1 向量召回

向量召回沿用现有QdrantRetriever。

输入query：

```text
original
rewrite
expansion
```

说明：

- 原始问题保留用户真实表达
- rewrite提高语义完整性
- expansion覆盖同义表达和相关检索方向

向量召回适合：

- 语义相近但字面不一致的问题
- 用户表达口语化的问题
- 文档中没有完全相同关键词但含义相关的问题

### 4.2 BM25召回

BM25召回使用Bluge，只使用原始用户问题。

原因：

- BM25依赖字面关键词
- 原始问题更容易保留字段名、错误码、接口名、配置项等精确词
- LLM改写可能会稀释或替换用户输入的关键字
- expansion可能引入过宽关键词，增加噪声

示例：

```text
用户问题:
notify_url没收到怎么办？

向量query:
notify_url没收到怎么办？
支付回调通知地址无法接收回调时如何排查？
支付回调通知失败的常见原因有哪些？

BM25query:
notify_url没收到怎么办？
```

这样BM25可以优先保留`notify_url`这类原始关键词。

## 5. Bluge索引设计

### 5.1 索引对象

Bluge只索引`document_child_chunks`。

原因：

```text
child_chunk用于检索
parent_chunk用于LLM阅读
```

所以BM25和向量检索保持同一粒度：

```text
BM25命中child_chunk
-> 回查parent_chunk
-> parent进入Prompt
```

### 5.2 索引字段

每个child_chunk写入一个Bluge文档。

建议字段：

```text
child_chunk_id
parent_chunk_id
document_id
knowledge_base_id
user_id
chunk_index
section_path
content
```

检索字段：

```text
section_path
content
```

过滤字段：

```text
user_id
knowledge_base_id
```

说明：

- `content`用于正文关键词检索
- `section_path`用于增强标题和章节关键词匹配
- `user_id`和`knowledge_base_id`用于隔离用户和知识库范围
- `child_chunk_id`用于回查MySQL

### 5.3 索引目录

建议新增配置：

```yaml
search:
  bluge_dir: "storage/bluge"
```

也可以放在`rag`下：

```yaml
rag:
  bm25_enabled: true
  bm25_top_k: 5
  rrf_k: 60
```

推荐配置：

```yaml
rag:
  hybrid_enabled: true
  bm25_enabled: true
  bm25_top_k: 5
  rrf_k: 60

search:
  bluge_dir: "storage/bluge"
```

## 6. 索引流程调整

当前v0.3.2索引流程：

```text
解析文档
-> 父子切片
-> child生成Embedding
-> child写MySQL
-> child向量写Qdrant
```

v0.3.3调整为：

```text
解析文档
-> 父子切片
-> parent写MySQL
-> child写MySQL
-> child生成Embedding并写Qdrant
-> child文本写Bluge
```

注意：

- Qdrant写入失败时文档索引应失败
- Bluge写入失败时文档索引也应失败
- 重建索引前必须先删除旧Qdrant向量和旧Bluge文档
- 删除文档时必须同步删除Bluge索引

## 7. 模块设计

### 7.1 新增bm25包或search包

建议新增：

```text
internal/search/bluge_store.go
```

核心职责：

```text
打开Bluge索引
写入child_chunk文档
删除child_chunk文档
按关键词搜索child_chunk
关闭索引资源
```

建议接口：

```go
type KeywordStore interface {
	IndexChild(ctx context.Context, child model.DocumentChildChunk) error
	DeleteByDocumentID(ctx context.Context, userID, documentID uint64) error
	Search(ctx context.Context, req KeywordSearchRequest) ([]KeywordHit, error)
}
```

请求结构：

```go
type KeywordSearchRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Query           string
	Limit           int
}
```

返回结构：

```go
type KeywordHit struct {
	ChildChunkID uint64
	Score        float64
	Rank         int
}
```

### 7.2 BM25Retriever

新增：

```text
internal/rag/bm25_retriever.go
```

职责：

```text
调用Bluge关键词检索
拿到child_chunk_id
回查MySQL组装RetrievedChunk
标记召回来源为bm25
```

### 7.3 HybridRetriever

新增：

```text
internal/rag/hybrid_retriever.go
```

职责：

```text
调用向量召回
调用BM25召回
使用RRF合并
返回统一RetrievedChunk列表
```

HybridRetriever建议成为RAGChain依赖的Retriever。

也就是：

```text
RAGChain
-> HybridRetriever
   -> QdrantRetriever
   -> BM25Retriever
```

## 8. RRF融合策略

RRF全称Reciprocal Rank Fusion。

公式：

```text
rrf_score = sum(1 / (rrf_k + rank))
```

其中：

```text
rrf_k默认60
rank从1开始
```

示例：

```text
child 10:
vector rank=1 -> 1/(60+1)
bm25 rank=4 -> 1/(60+4)

final_rrf_score = 1/61 + 1/64
```

为什么使用RRF：

- 向量score和BM25score量纲不同，不能直接相加
- RRF只依赖排名，不依赖不同召回器的原始分数
- 多路都排名靠前的结果会自然排到前面
- 实现简单，适合MVP阶段

v0.3.3最终排序使用`rrf_score`。

原始分数仍然保留在trace中：

```text
vector_score
bm25_score
rrf_score
```

## 9. Retrieve流程调整

v0.3.2中多query检索在RAGChain的`retrieveNode`中完成。

v0.3.3建议调整为：

```text
RAGChain仍然负责QueryRewrite
HybridRetriever负责召回融合
```

RAGChain传入：

```text
全部rewritten_queries
原始用户问题
```

HybridRetriever内部执行：

```text
1. 遍历rewritten_queries执行VectorRetriever
2. 使用original_question执行BM25Retriever
3. 将两路结果按child_chunk_id合并
4. 记录每个child的recall_sources
5. 记录每个child的matched_queries
6. 计算RRF分数
7. 按RRF分数降序返回
```

如果BM25失败：

```text
记录trace错误
向量召回继续
```

如果向量召回失败：

```text
返回错误
```

说明：向量召回仍是主召回链路，BM25作为增强召回。

## 10. 类型调整

`RetrievedChunk`建议新增：

```go
RecallSources []string
VectorScore   float64
BM25Score     float64
RRFScore      float64
```

`RecallSources`取值：

```text
vector
bm25
```

`TraceHit`建议新增：

```go
RecallSources []string `json:"recall_sources"`
VectorScore   float64  `json:"vector_score,omitempty"`
BM25Score     float64  `json:"bm25_score,omitempty"`
RRFScore      float64  `json:"rrf_score,omitempty"`
```

`RetrievalTrace`建议新增：

```go
HybridEnabled bool `json:"hybrid_enabled"`
BM25Enabled   bool `json:"bm25_enabled"`
RRFK          int  `json:"rrf_k"`
BM25Error     string `json:"bm25_error,omitempty"`
```

## 11. API调整

Chat请求可以新增可选字段：

```json
{
  "question": "notify_url没收到怎么办？",
  "query_rewrite": true,
  "hybrid": true,
  "bm25_top_k": 5
}
```

说明：

- `hybrid`不传时使用配置默认值
- `hybrid=false`时退化为纯向量检索
- `bm25_top_k`不传时使用配置默认值

普通响应结构不强制变化。

详细召回来源进入`retrieval_trace`。

## 12. Trace示例

```json
{
  "query": "notify_url没收到怎么办？",
  "rewrite_enabled": true,
  "hybrid_enabled": true,
  "bm25_enabled": true,
  "rewritten_queries": [
    {
      "query": "notify_url没收到怎么办？",
      "type": "original"
    },
    {
      "query": "支付回调通知地址无法接收回调时如何排查？",
      "type": "rewrite"
    }
  ],
  "hits": [
    {
      "chunk_id": 12,
      "parent_chunk_id": 3,
      "recall_sources": ["vector", "bm25"],
      "matched_queries": [
        "支付回调通知地址无法接收回调时如何排查？"
      ],
      "vector_score": 0.81,
      "bm25_score": 4.32,
      "rrf_score": 0.032,
      "used": true
    }
  ]
}
```

## 13. 删除和重建索引

文档删除时：

```text
删除Qdrant向量
-> 删除Bluge索引文档
-> 删除MySQL父子切片
-> 删除documents记录
-> 删除本地文件
```

文档重建索引时：

```text
删除旧Qdrant向量
-> 删除旧Bluge索引文档
-> 删除旧MySQL父子切片
-> 重新解析和切片
-> 重新写入MySQL、Qdrant、Bluge
```

## 14. 开发顺序

建议按以下顺序实现：

```text
1. 引入Bluge依赖
2. 新增search配置和RAG混合检索配置
3. 新增internal/search包封装Bluge读写
4. 在Indexer中写入child_chunk后同步写Bluge
5. 在文档删除和重建索引中同步删除Bluge文档
6. 新增BM25Retriever
7. 抽取childID回查MySQL组装RetrievedChunk的公共逻辑
8. 新增HybridRetriever
9. 实现RRF合并
10. 调整RAGChain和RetrieveRequest以支持多query和原问题
11. 增强RetrievedChunk和TraceHit
12. 普通Chat链路联调
13. SSE链路联调
14. 验证BM25失败时向量召回仍可继续
```

## 15. 验收标准

v0.3.3完成后需要满足：

- 文档索引时child_chunk能写入Bluge
- 删除文档时Bluge索引能同步删除
- 重建索引时旧Bluge数据不会残留
- BM25Retriever能根据原始问题召回child_chunk
- Qdrant向量召回仍然使用原问题、rewrite和expansion
- HybridRetriever能融合向量召回和BM25召回
- RRF合并能按child_chunk_id去重
- 多路都命中的child能排在更靠前位置
- ContextBuilder仍然使用parent_chunk进入Prompt
- 同一个parent不会重复进入Prompt
- retrieval_trace能看到recall_sources
- retrieval_trace能看到vector_score、bm25_score和rrf_score
- 普通Chat和SSE Chat都能正常工作
- hybrid关闭后能退化为纯向量检索

## 16. 面试表达重点

v0.3.3可以重点讲：

- 为什么RAG召回不能只依赖向量检索
- 向量检索和BM25关键词检索分别解决什么问题
- 为什么BM25只使用用户原始问题而不是所有rewrite query
- 为什么使用Bluge而不是直接上Elasticsearch
- 为什么RRF适合融合不同召回器的结果
- 父子切片如何和Hybrid Retrieval结合
- 如何通过trace解释一个chunk来自向量召回、关键词召回还是两路共同命中

