package rag

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"OurAgent/internal/document"

	einomodel "github.com/cloudwego/eino/components/model"
	einoprompt "github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	pkgerrors "github.com/pkg/errors"
)

const systemPromptTemplate = `你是企业知识库问答助手。
请只根据给定的知识库上下文回答问题。
如果上下文中没有足够信息，请回答“根据当前知识库内容无法确认”。
不要编造上下文中不存在的信息。
回答应简洁、清晰，并尽量按条目组织。`

type RAGChain struct {
	retriever   Retriever
	rewriter    QueryRewriter
	reranker    Reranker
	chat        einomodel.BaseChatModel
	modelName   string
	rerankModel string
	template    *einoprompt.DefaultChatTemplate
	invoke      compose.Runnable[Request, *PreparedChat]
	stream      compose.Runnable[Request, StreamChunk]
}

type chainState struct {
	req           Request
	queries       []RewrittenQuery
	results       []RetrievedChunk
	contextText   string
	sources       []Source
	trace         RetrievalTrace
	promptPreview string
	messages      []*schema.Message
	answer        string
	promptTokens  int
	outputTokens  int
}

func NewRAGChain(ctx context.Context, retriever Retriever, rewriter QueryRewriter, reranker Reranker, chat einomodel.BaseChatModel, modelName, rerankModel string) (*RAGChain, error) {
	if rewriter == nil {
		rewriter = NewFallbackQueryRewriter()
	}
	if reranker == nil {
		reranker = NewFallbackReranker()
	}
	// chain容器
	chain := &RAGChain{
		retriever:   retriever,
		rewriter:    rewriter,
		reranker:    reranker,
		chat:        chat,
		modelName:   modelName,
		rerankModel: rerankModel,
		template: einoprompt.FromMessages(
			schema.FString,
			schema.SystemMessage(systemPromptTemplate),
			schema.UserMessage("知识库上下文：\n{context}\n\n用户问题：\n{question}"),
		),
	}

	// 普通问答链路
	invoke, err := compose.NewChain[Request, *PreparedChat]().
		AppendLambda(compose.InvokableLambda(chain.queryRewriteNode, compose.WithLambdaType("QueryRewriteNode")), compose.WithNodeName("Query Rewrite Node")).
		AppendLambda(compose.InvokableLambda(chain.retrieveNode, compose.WithLambdaType("RetrieverNode")), compose.WithNodeName("Retriever Node")).
		AppendLambda(compose.InvokableLambda(chain.rerankNode, compose.WithLambdaType("RerankNode")), compose.WithNodeName("Rerank Node")).
		AppendLambda(compose.InvokableLambda(chain.contextBuilderNode, compose.WithLambdaType("ContextBuilderNode")), compose.WithNodeName("Context Builder Node")).
		AppendLambda(compose.InvokableLambda(chain.promptTemplateNode, compose.WithLambdaType("PromptTemplateNode")), compose.WithNodeName("Prompt Template Node")).
		AppendLambda(compose.InvokableLambda(chain.chatModelNode, compose.WithLambdaType("ChatModelNode")), compose.WithNodeName("ChatModel Node")).
		AppendLambda(compose.InvokableLambda(chain.outputAssemblerNode, compose.WithLambdaType("OutputAssemblerNode")), compose.WithNodeName("Output Assembler Node")).
		Compile(ctx)
	if err != nil {
		return nil, err
	}
	// 流式问答链路
	stream, err := compose.NewChain[Request, StreamChunk]().
		AppendLambda(compose.InvokableLambda(chain.queryRewriteNode, compose.WithLambdaType("QueryRewriteNode")), compose.WithNodeName("Query Rewrite Node")).
		AppendLambda(compose.InvokableLambda(chain.retrieveNode, compose.WithLambdaType("RetrieverNode")), compose.WithNodeName("Retriever Node")).
		AppendLambda(compose.InvokableLambda(chain.rerankNode, compose.WithLambdaType("RerankNode")), compose.WithNodeName("Rerank Node")).
		AppendLambda(compose.InvokableLambda(chain.contextBuilderNode, compose.WithLambdaType("ContextBuilderNode")), compose.WithNodeName("Context Builder Node")).
		AppendLambda(compose.InvokableLambda(chain.promptTemplateNode, compose.WithLambdaType("PromptTemplateNode")), compose.WithNodeName("Prompt Template Node")).
		AppendLambda(compose.StreamableLambda(chain.streamChatModelNode, compose.WithLambdaType("StreamChatModelNode")), compose.WithNodeName("Stream ChatModel Node")).
		Compile(ctx)
	if err != nil {
		return nil, err
	}
	// 将两个链路存入RAGChain
	chain.invoke = invoke
	chain.stream = stream
	return chain, nil
}

// Invoke 执行Eino RAGChain并返回完整回答
func (c *RAGChain) Invoke(ctx context.Context, req Request) (*PreparedChat, error) {
	return c.invoke.Invoke(ctx, req)
}

// Stream 执行Eino RAGChain并返回流式回答
func (c *RAGChain) Stream(ctx context.Context, req Request) (*schema.StreamReader[StreamChunk], error) {
	return c.stream.Stream(ctx, req)
}

// ModelName 返回当前Chat模型名称
func (c *RAGChain) ModelName() string {
	return c.modelName
}

// queryRewriteNode 生成用于检索的query列表
func (c *RAGChain) queryRewriteNode(ctx context.Context, req Request) (*chainState, error) {
	trace := NewTrace(req, "")

	// 根据请求开关选择真实改写器或兜底改写器
	rewriter := c.rewriter
	if !req.QueryRewrite {
		rewriter = NewFallbackQueryRewriter()
	}

	// 调用改写器生成原问题、改写问题和扩展问题
	result, err := rewriter.Rewrite(ctx, RewriteRequest{
		UserID:          req.UserID,
		KnowledgeBaseID: req.KnowledgeBaseID,
		Question:        req.Question,
		MaxQueries:      req.QueryRewriteMaxQueries,
		IncludeOriginal: req.QueryRewriteIncludeOriginal,
	})
	if err != nil {
		// 改写失败时记录trace，并退化为只用原问题检索
		trace.RewriteError = err.Error()
		result, _ = NewFallbackQueryRewriter().Rewrite(ctx, RewriteRequest{
			Question:        req.Question,
			MaxQueries:      1,
			IncludeOriginal: true,
		})
	}

	// 规范化query列表，并写入trace用于排查检索来源
	queries := normalizeRewriteResult(req.Question, result)
	trace.RewrittenQueries = traceQueries(queries)
	return &chainState{req: req, queries: queries, trace: trace}, nil
}

// retrieveNode 执行多query检索并合并结果
func (c *RAGChain) retrieveNode(ctx context.Context, state *chainState) (*chainState, error) {
	results, err := c.retriever.Retrieve(ctx, RetrieveRequest{
		UserID:          state.req.UserID,
		KnowledgeBaseID: state.req.KnowledgeBaseID,
		Query:           state.req.Question,
		Queries:         state.queries,
		TopK:            state.req.TopK,
		BM25TopK:        state.req.BM25TopK,
		HybridEnabled:   state.req.HybridEnabled,
		BM25Enabled:     state.req.BM25Enabled,
		RRFK:            state.req.RRFK,
		Trace:           &state.trace,
	})
	if err != nil {
		return nil, err
	}
	state.results = results
	return state, nil
}

// rerankNode 对召回候选切片进行精排
func (c *RAGChain) rerankNode(ctx context.Context, state *chainState) (*chainState, error) {
	// 未开启精排或没有召回结果时直接沿用原结果
	if !state.req.Rerank || len(state.results) == 0 {
		return state, nil
	}

	// 限制送入精排模型的候选数量和最终保留数量
	candidateLimit := state.req.RerankCandidateLimit
	if candidateLimit <= 0 || candidateLimit > len(state.results) {
		candidateLimit = len(state.results)
	}
	topN := state.req.RerankTopN
	if topN <= 0 || topN > candidateLimit {
		topN = candidateLimit
	}
	state.trace.RerankEnabled = true
	state.trace.RerankModel = c.rerankModel
	state.trace.RerankCandidateCount = candidateLimit

	// 记录精排前排名，并组装模型需要的候选文本
	items := make([]RerankItem, 0, candidateLimit)
	for i := 0; i < candidateLimit; i++ {
		state.results[i].BeforeRerankRank = i + 1
		items = append(items, RerankItem{
			Index:         i,
			ChildChunkID:  state.results[i].MatchedChunk.ID,
			ParentChunkID: state.results[i].Chunk.ID,
			Text:          buildRerankText(state.results[i]),
		})
	}

	// 调用精排模型判断问题和候选切片的相关性
	result, err := c.reranker.Rerank(ctx, RerankRequest{
		Query: state.req.Question,
		Items: items,
		TopN:  topN,
	})
	if err != nil {
		state.trace.RerankError = err.Error()
		return state, nil
	}

	// 用精排结果重排候选，后续上下文构建只读取重排后的结果
	reranked := applyRerankResult(state.results[:candidateLimit], result, topN)
	state.results = reranked
	return state, nil
}

// contextBuilderNode 构建上下文和检索trace
func (c *RAGChain) contextBuilderNode(_ context.Context, state *chainState) (*chainState, error) {
	var builder strings.Builder
	state.sources = make([]Source, 0, len(state.results))
	usedTokens := 0
	usedParentIDs := make(map[uint64]struct{})

	for _, item := range state.results {
		// 获取切片token数，缺失时按正文估算
		tokens := item.Chunk.TokenCount
		if tokens == 0 {
			tokens = document.EstimateTokens(item.Chunk.Content)
		}

		// 先记录命中切片的基础trace信息
		hit := TraceHit{
			ChunkID:          item.MatchedChunk.ID,
			DocumentID:       item.Chunk.DocumentID,
			DocumentName:     item.Document.Filename,
			SectionPath:      item.MatchedChunk.SectionPath,
			ParentChunkID:    item.Chunk.ID,
			ChunkIndex:       item.MatchedChunk.ChunkIndex,
			Score:            item.Score,
			MatchedQueries:   item.MatchedQueries,
			RecallSources:    item.RecallSources,
			VectorScore:      item.VectorScore,
			BM25Score:        item.BM25Score,
			RRFScore:         item.RRFScore,
			RerankScore:      item.RerankScore,
			RerankRank:       item.RerankRank,
			BeforeRerankRank: item.BeforeRerankRank,
		}

		// 过滤低于相似度阈值的切片
		if item.Score < state.req.ScoreThreshold {
			hit.Reason = "低于相似度阈值"
			state.trace.FilteredCount++
			state.trace.Hits = append(state.trace.Hits, hit)
			continue
		}

		// 过滤超过上下文token上限的切片
		if usedTokens > 0 && usedTokens+tokens > state.req.MaxContextTokens {
			hit.Reason = "超过上下文 token 上限"
			state.trace.FilteredCount++
			state.trace.Hits = append(state.trace.Hits, hit)
			continue
		}
		// 过滤重复父chunk
		if _, exists := usedParentIDs[item.Chunk.ID]; exists {
			hit.Reason = "父chunk已进入上下文"
			state.trace.FilteredCount++
			state.trace.Hits = append(state.trace.Hits, hit)
			continue
		}

		// 将可用切片写入上下文并记录来源
		hit.Used = true
		state.trace.UsedChunkCount++
		state.trace.ContextTokenCount += tokens
		state.trace.Hits = append(state.trace.Hits, hit)
		usedParentIDs[item.Chunk.ID] = struct{}{}
		builder.WriteString(buildSourceBlock(len(state.sources)+1, item))
		usedTokens += tokens

		state.sources = append(state.sources, Source{
			SourceType:     SourceTypeKnowledgeBase,
			DocumentID:     item.Chunk.DocumentID,
			DocumentName:   item.Document.Filename,
			SectionPath:    item.MatchedChunk.SectionPath,
			ParentChunkID:  item.Chunk.ID,
			ChunkID:        item.MatchedChunk.ID,
			ChunkIndex:     item.MatchedChunk.ChunkIndex,
			Score:          item.Score,
			ContentPreview: preview(item.MatchedChunk.Content, 160),
		})
	}

	// 记录无法生成可靠上下文的原因
	if len(state.results) == 0 {
		state.trace.RejectReason = "没有检索到切片"
	} else if state.trace.UsedChunkCount == 0 {
		state.trace.RejectReason = "没有达到置信度阈值的切片"
	}

	// 生成最终上下文和prompt预览
	state.contextText = strings.TrimSpace(builder.String())
	state.promptPreview = previewPrompt(state.contextText, state.req.Question, 2000)
	return state, nil
}

// promptTemplateNode 渲染模型消息
func (c *RAGChain) promptTemplateNode(ctx context.Context, state *chainState) (*chainState, error) {
	//strict模式且没有合格chunk，拒答
	if state.req.StrictMode && state.trace.UsedChunkCount == 0 {
		state.answer = FallbackAnswer
		return state, nil
	}
	if len(state.results) == 0 || strings.TrimSpace(state.contextText) == "" {
		if state.trace.RejectReason == "" {
			state.trace.RejectReason = "没有检索到可用切片"
		}
		state.answer = FallbackAnswer
		return state, nil
	}

	messages, err := c.template.Format(ctx, map[string]any{
		"context":  state.contextText,
		"question": state.req.Question,
	})
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "渲染 prompt 模板失败")
	}
	state.messages = messages
	return state, nil
}

// chatModelNode 调用Chat模型生成完整回答
func (c *RAGChain) chatModelNode(ctx context.Context, state *chainState) (*chainState, error) {
	if state.answer != "" {
		return state, nil
	}
	resp, err := c.chat.Generate(ctx, state.messages)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "调用 chat 模型失败")
	}
	state.answer = strings.TrimSpace(resp.Content)
	if state.answer == "" {
		state.answer = FallbackAnswer
	}
	if resp.ResponseMeta != nil && resp.ResponseMeta.Usage != nil {
		state.promptTokens = resp.ResponseMeta.Usage.PromptTokens
		state.outputTokens = resp.ResponseMeta.Usage.CompletionTokens
	}
	return state, nil
}

// outputAssemblerNode 组装完整问答结果
func (c *RAGChain) outputAssemblerNode(_ context.Context, state *chainState) (*PreparedChat, error) {
	if state.answer == "" {
		state.answer = FallbackAnswer
	}
	return state.toPrepared(), nil
}

// streamChatModelNode 调用Chat模型生成流式回答
func (c *RAGChain) streamChatModelNode(ctx context.Context, state *chainState) (*schema.StreamReader[StreamChunk], error) {
	// 创建Eino流式管道，writer写入结果，reader交给外部读取
	reader, writer := schema.Pipe[StreamChunk](4)
	go func() {
		defer writer.Close()

		// 前置节点已经拒答时，直接返回拒答内容和最终结果
		if state.answer != "" {
			writer.Send(StreamChunk{Content: state.answer}, nil)
			writer.Send(StreamChunk{Prepared: state.toPrepared()}, nil)
			return
		}

		// 调用Chat模型流式接口
		stream, err := c.chat.Stream(ctx, state.messages)
		if err != nil {
			writer.Send(StreamChunk{}, pkgerrors.WithMessage(err, "调用 chat 模型失败"))
			return
		}
		defer stream.Close()

		// 持续读取模型输出，一边发送片段一边拼接完整答案
		var builder strings.Builder
		for {
			chunk, err := stream.Recv()
			if err != nil {
				if stderrors.Is(err, io.EOF) {
					break
				}
				writer.Send(StreamChunk{}, pkgerrors.WithMessage(err, "读取 chat 流失败"))
				return
			}
			builder.WriteString(chunk.Content)
			writer.Send(StreamChunk{Content: chunk.Content}, nil)
		}

		// 流结束后整理完整答案
		state.answer = strings.TrimSpace(builder.String())
		if state.answer == "" {
			state.answer = FallbackAnswer
		}

		// 发送最终结果，用于保存日志和返回来源
		writer.Send(StreamChunk{Prepared: state.toPrepared()}, nil)
	}()
	return reader, nil
}

func (s *chainState) toPrepared() *PreparedChat {
	return &PreparedChat{
		Request:          s.req,
		Messages:         s.messages,
		Answer:           s.answer,
		Sources:          s.sources,
		Trace:            s.trace,
		PromptPreview:    s.promptPreview,
		ContextText:      s.contextText,
		PromptTokens:     s.promptTokens,
		CompletionTokens: s.outputTokens,
	}
}

func previewPrompt(contextText, question string, max int) string {
	text := fmt.Sprintf("知识库上下文：\n%s\n\n用户问题：\n%s", contextText, question)
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

func buildSourceBlock(index int, item RetrievedChunk) string {
	if strings.TrimSpace(item.Chunk.SectionPath) != "" {
		return fmt.Sprintf("[来源 %d: %s / %s / chunk %d]\n%s\n\n", index, item.Document.Filename, item.Chunk.SectionPath, item.Chunk.ChunkIndex, item.Chunk.Content)
	}
	return fmt.Sprintf("[来源 %d: %s / chunk %d]\n%s\n\n", index, item.Document.Filename, item.Chunk.ChunkIndex, item.Chunk.Content)
}

func buildRerankText(item RetrievedChunk) string {
	var builder strings.Builder
	if strings.TrimSpace(item.MatchedChunk.SectionPath) != "" {
		builder.WriteString("章节：")
		builder.WriteString(item.MatchedChunk.SectionPath)
		builder.WriteString("\n")
	}
	builder.WriteString("内容：")
	builder.WriteString(item.MatchedChunk.Content)
	return builder.String()
}

func applyRerankResult(candidates []RetrievedChunk, result *RerankResult, topN int) []RetrievedChunk {
	if result == nil || len(result.Items) == 0 {
		return candidates
	}

	// 按模型返回的index找到原候选，并写入精排分数
	reranked := make([]RetrievedChunk, 0, len(result.Items))
	seen := make(map[int]struct{}, len(result.Items))
	for _, resultItem := range result.Items {
		if resultItem.Index < 0 || resultItem.Index >= len(candidates) {
			continue
		}
		if _, ok := seen[resultItem.Index]; ok {
			continue
		}
		seen[resultItem.Index] = struct{}{}
		item := candidates[resultItem.Index]
		item.RerankScore = resultItem.Score
		item.Score = resultItem.Score
		reranked = append(reranked, item)
	}

	// 按精排分数重新排序，分数越高越靠前
	sort.SliceStable(reranked, func(i, j int) bool {
		return reranked[i].RerankScore > reranked[j].RerankScore
	})

	// 只保留精排后的前topN个结果进入后续上下文构建
	if topN > 0 && topN < len(reranked) {
		reranked = reranked[:topN]
	}

	// 记录精排后的最终排名，便于trace排查
	for i := range reranked {
		reranked[i].RerankRank = i + 1
	}
	return reranked
}

func preview(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

func normalizeRewriteResult(question string, result *RewriteResult) []RewrittenQuery {
	if result == nil {
		return []RewrittenQuery{{Query: strings.TrimSpace(question), Type: QueryTypeOriginal}}
	}
	queries := dedupeRewrittenQueries(result.Queries)
	if len(queries) == 0 {
		queries = append(queries, RewrittenQuery{Query: strings.TrimSpace(question), Type: QueryTypeOriginal})
	}
	return queries
}

func traceQueries(queries []RewrittenQuery) []TraceQuery {
	items := make([]TraceQuery, 0, len(queries))
	for _, query := range queries {
		items = append(items, TraceQuery{
			Query:  query.Query,
			Type:   query.Type,
			Reason: query.Reason,
		})
	}
	return items
}

func appendQueries(existing []string, queries []string) []string {
	for _, query := range queries {
		existing = appendQuery(existing, query)
	}
	return existing
}

func appendQuery(existing []string, query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return existing
	}
	for _, item := range existing {
		if item == query {
			return existing
		}
	}
	return append(existing, query)
}
