# MVP 0.3.6 联网搜索降级实现说明

## 目标

MVP 0.3.6 在现有知识库RAG能力基础上增加联网搜索降级能力。

当用户问题无法从当前知识库中找到可靠上下文时，系统可以选择性调用Qwen模型的联网搜索能力生成降级回答，并明确提示用户：该回答不是来自知识库，网络资料可能不准确、过期或与实际情况不一致。

本版本不把联网搜索作为常规知识库召回的一部分，也不重构当前Eino Chain为Graph。联网搜索只作为业务层fallback，由`ChatService`在RAG拒答后触发。

核心目标：

- 保持现有知识库RAG主链路稳定，不影响正常知识库问答
- 只在知识库没有可用上下文时启用联网搜索
- 联网回答必须带有明确免责声明
- 优先使用DashScope原生接口，获取联网搜索来源
- 日志和返回结果区分知识库来源与网络来源

## 当前问题

当前RAG流程是：

```text
ChatService
-> RAGChain
   -> queryRewrite
   -> hybrid retrieve: Qdrant + BM25
   -> rerank
   -> contextBuilder
   -> prompt
   -> chat model
-> saveLog
```

当知识库没有命中内容时，系统返回固定拒答：

```text
根据当前知识库内容无法确认。
```

这个策略能避免幻觉，但用户体验上有一个明显缺口：

- 用户可能只是想问实时信息、公开资料或知识库外的通用问题
- 当前系统即使模型本身支持联网搜索，也不会使用
- 用户无法区分“知识库没有答案”和“系统完全不能回答”

因此需要增加一个受控的降级分支：

```text
知识库查不到
-> 联网搜索
-> 明确标注非知识库答案
-> 返回降级回答
```

## 设计原则

### 不混淆知识源

联网搜索结果不能伪装成知识库内容。

回答中必须明确说明：

```text
当前知识库没有找到足够信息，以下内容基于联网搜索结果生成，仅供参考。网络资料可能不准确、过期或与实际情况不一致。
```

### 不进入知识库索引

联网搜索结果只用于当前问答，不写入：

- documents
- parent chunks
- child chunks
- Qdrant
- Bluge

后续如果需要把搜索结果沉淀到知识库，应做成独立的“保存为文档”功能，而不是自动入库。

### 不作为常规第三路召回

MVP 0.3.6 不做：

```text
Qdrant + BM25 + WebSearch统一召回
```

原因是知识库召回和公网搜索的可信边界不同。当前版本只做fallback：

```text
知识库有答案：只使用知识库
知识库无答案：才使用联网搜索
```

### 不重构Graph

当前Eino Chain继续保持线性知识库RAG职责。联网降级先放在`ChatService`层编排：

```text
ChatService
-> RAGChain
-> 如果RAGChain拒答且开启web fallback
   -> WebFallbackAnswerer
-> saveLog
```

后续如果要把知识库回答、联网回答、多策略路由统一可视化，再考虑升级为Eino Graph。

## 触发条件

联网降级只在以下条件全部满足时触发：

```text
web_search.enabled = true
web_search.fallback_only = true
RAGChain返回拒答
RAG trace显示没有可用知识库上下文
```

推荐判断逻辑：

```text
prepared.Answer == rag.FallbackAnswer
prepared.Trace.UsedChunkCount == 0
```

如果知识库没有已完成索引文档，`ChatService.validate`提前返回拒答，也可以触发联网降级。

不触发联网降级的情况：

- 用户或配置关闭联网搜索
- 知识库已有可用chunk
- 知识库召回失败是系统错误
- 问题为空或知识库权限校验失败
- strict策略后续明确要求只允许知识库回答

## 整体流程

非流式问答：

```text
ChatService.Chat
-> validate
-> RAGChain.Invoke
-> 判断是否需要web fallback
   -> 否：保存知识库回答日志并返回
   -> 是：调用DashScopeWebFallbackAnswerer.Answer
-> 用联网回答覆盖prepared.Answer
-> 写入web sources和trace
-> saveLog
-> 返回回答
```

流式问答：

```text
ChatService.Stream
-> validate
-> 如果validate提前拒答且允许web fallback
   -> 调用DashScope联网降级
   -> 以message事件发送完整降级回答
   -> 发送sources和done
-> 否则继续现有RAG流式流程
-> RAG流式结束后拿到final PreparedChat
-> 如果final需要web fallback
   -> 调用DashScope联网降级
   -> 发送联网降级回答
   -> 发送sources和done
```

MVP阶段可以接受联网降级不逐字流式输出，先把完整回答作为一条`message`事件发送。

## DashScope原生联网接口

当前项目的普通ChatModel走OpenAI-compatible接口：

```text
https://dashscope.aliyuncs.com/compatible-mode/v1
```

这个接口适合接Eino，但Chat Completions开启`enable_search`后通常拿不到结构化搜索来源。

MVP 0.3.6 为联网降级新增DashScope原生接口客户端，只在fallback时使用。目标是拿到：

- 联网回答文本
- 搜索结果标题
- 搜索结果URL
- 搜索结果摘要

接口封装建议：

```go
type WebFallbackAnswerer interface {
	Answer(ctx context.Context, req WebFallbackRequest) (*WebFallbackResult, error)
}

type WebFallbackRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
}

type WebFallbackResult struct {
	Answer  string
	Sources []WebSource
}

type WebSource struct {
	Title   string
	URL     string
	Snippet string
}
```

实现类：

```text
internal/websearch/dashscope.go
```

或：

```text
internal/rag/web_fallback.go
```

推荐放在`internal/websearch`，避免把公网搜索能力和知识库RAG强绑定。

## Prompt策略

联网降级回答必须使用独立prompt，不复用知识库prompt。

推荐system prompt：

```text
你是联网搜索降级回答助手。
当前用户的知识库没有找到足够信息，你可以基于联网搜索结果回答。
回答必须清楚说明信息来自网络搜索，仅供参考。
不要声称这些信息来自用户知识库。
如果网络结果不足或不确定，请直接说明不确定。
```

推荐用户prompt结构：

```text
用户问题：
{question}

请基于联网搜索结果回答。回答开头必须包含：
当前知识库没有找到足够信息，以下内容基于联网搜索结果生成，仅供参考。网络资料可能不准确、过期或与实际情况不一致。
```

如果DashScope接口支持直接传`enable_search`和`enable_source`，则由模型自行联网搜索。

## 返回结构

当前`rag.Source`主要表示知识库来源。MVP 0.3.6建议扩展来源类型：

```go
type Source struct {
	SourceType     string  `json:"source_type"`
	DocumentID     uint64  `json:"document_id,omitempty"`
	DocumentName   string  `json:"document_name,omitempty"`
	SectionPath    string  `json:"section_path,omitempty"`
	ParentChunkID  uint64  `json:"parent_chunk_id,omitempty"`
	ChunkID        uint64  `json:"chunk_id,omitempty"`
	ChunkIndex     int     `json:"chunk_index,omitempty"`
	Score          float64 `json:"score"`
	ContentPreview string  `json:"content_preview"`
	Title          string  `json:"title,omitempty"`
	URL            string  `json:"url,omitempty"`
}
```

知识库来源：

```text
source_type = knowledge_base
document_id / chunk_id / document_name正常填充
```

网络来源：

```text
source_type = web
title / url / content_preview正常填充
```

如果前端暂时不展示网络来源，也应该在日志中保留，方便后续排查。

## Trace设计

在`RetrievalTrace`中增加联网降级字段：

```go
WebFallbackEnabled bool          `json:"web_fallback_enabled"`
WebFallbackUsed    bool          `json:"web_fallback_used"`
WebFallbackReason  string        `json:"web_fallback_reason,omitempty"`
WebSearchProvider  string        `json:"web_search_provider,omitempty"`
WebSearchModel     string        `json:"web_search_model,omitempty"`
WebSearchResultCount int         `json:"web_search_result_count,omitempty"`
WebSearchError     string        `json:"web_search_error,omitempty"`
```

触发时记录：

```text
web_fallback_used = true
web_fallback_reason = trace.reject_reason
web_search_provider = dashscope
web_search_model = qwen3.6-plus
web_search_result_count = len(sources)
```

失败时记录`web_search_error`，并保留原来的知识库拒答。

## 配置项

新增配置：

```yaml
web_search:
  enabled: false
  fallback_only: true
  provider: "dashscope"
  api_key: "${OPENAI_API_KEY}"
  model: "qwen3.6-plus"
  timeout_seconds: 8
  enable_source: true
  disclaimer: "当前知识库没有找到足够信息，以下内容基于联网搜索结果生成，仅供参考。网络资料可能不准确、过期或与实际情况不一致。"
```

默认建议：

```text
enabled = false
fallback_only = true
timeout_seconds = 8
enable_source = true
```

上线时由配置决定是否开启，避免默认把用户问题发送到公网搜索。

## 代码改动范围

新增：

```text
internal/websearch
  types.go              联网降级请求、结果、来源结构
  dashscope.go          DashScope原生联网搜索回答客户端
```

调整：

```text
internal/config/config.go
  增加WebSearchConfig

config.yaml
  增加web_search配置

internal/rag/types.go
  Source增加source_type/title/url
  RetrievalTrace增加web fallback字段

internal/service/chat_service.go
  注入WebFallbackAnswerer
  Chat和Stream在RAG拒答后触发联网降级

cmd/server/main.go
  根据配置初始化DashScopeWebFallbackAnswerer
  注入ChatService
```

可选调整：

```text
README.md
  补充联网降级能力说明和环境变量
```

## 错误处理

联网降级失败时，不影响原知识库拒答。

```text
知识库拒答
-> 尝试联网降级
-> 联网失败
-> 返回原知识库拒答
-> trace记录web_search_error
```

不能因为联网搜索失败而把整个问答接口变成500，除非原本RAG链路已经出现系统错误。

## 安全和边界

MVP 0.3.6保证：

- 只有知识库查不到时才触发联网搜索
- 联网回答不会冒充知识库回答
- 联网回答带有明确免责声明
- 联网结果不写入知识库索引
- 联网失败时保留原知识库拒答
- 日志记录是否触发联网降级

MVP 0.3.6暂不保证：

- 网络资料真实性
- 搜索来源一定完整返回
- 搜索结果逐条可审计
- 联网回答逐字流式输出
- 对网页内容做二次抓取和清洗
- 防御所有网页prompt injection

## 后续演进

后续可以按复杂度逐步增强：

```text
MVP 0.3.7
  前端展示web来源和免责声明

MVP 0.4
  将RAG Chain重构为Eino Graph
  把知识库回答、联网降级、拒答作为显式分支

MVP 0.4+
  如果需要真正第三路召回，再引入独立WebSearch Retriever
  支持Qdrant + BM25 + WebSearch统一融合和重排
```

