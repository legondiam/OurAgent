package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/document"
	"OurAgent/internal/llm"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	appsvc "OurAgent/internal/service"
	"OurAgent/internal/vectorstore"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/datatypes"
)

type Service struct {
	kbs      *repository.KnowledgeBaseRepository
	docs     *repository.DocumentRepository
	chunks   *repository.ChunkRepository
	logs     *repository.ChatLogRepository
	qdrant   *vectorstore.QdrantClient
	embedder llm.EmbeddingProvider
	chat     llm.ChatProvider
	cfg      *config.Config
}

type ChatRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
	TopK            int
}

type ChatResponse struct {
	Answer    string   `json:"answer"`
	Sources   []Source `json:"sources"`
	ChatLogID uint64   `json:"chat_log_id"`
}

type Source struct {
	DocumentID     uint64  `json:"document_id"`
	DocumentName   string  `json:"document_name"`
	ChunkID        uint64  `json:"chunk_id"`
	ChunkIndex     int     `json:"chunk_index"`
	Score          float64 `json:"score"`
	ContentPreview string  `json:"content_preview"`
}

func NewService(kbs *repository.KnowledgeBaseRepository, docs *repository.DocumentRepository, chunks *repository.ChunkRepository, logs *repository.ChatLogRepository, qdrant *vectorstore.QdrantClient, embedder llm.EmbeddingProvider, chat llm.ChatProvider, cfg *config.Config) *Service {
	return &Service{kbs: kbs, docs: docs, chunks: chunks, logs: logs, qdrant: qdrant, embedder: embedder, chat: chat, cfg: cfg}
}

// Chat 执行知识库检索并生成回答
func (s *Service) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	start := time.Now()
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		return nil, pkgerrors.WithStack(appsvc.ErrQuestionEmpty)
	}
	if req.TopK <= 0 {
		req.TopK = s.cfg.RAG.TopK
	}

	//确认知识库属于当前用户
	exists, err := s.kbs.ExistsByIDAndUserID(req.KnowledgeBaseID, req.UserID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	if !exists {
		return nil, pkgerrors.WithStack(appsvc.ErrKnowledgeBaseNotFound)
	}
	//确认知识库中有已完成索引的文档
	completedDocs, err := s.docs.CountCompleted(req.UserID, req.KnowledgeBaseID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询已完成索引文档失败")
	}
	if completedDocs == 0 {
		resp := &ChatResponse{Answer: "根据当前知识库内容无法确认。", Sources: []Source{}}
		logID, _ := s.saveLog(req, resp.Answer, resp.Sources, start, 0, 0)
		resp.ChatLogID = logID
		return resp, nil
	}

	//用户问题向量化
	vectors, err := s.embedder.Embed(ctx, []string{req.Question})
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "问题向量化失败")
	}
	//搜索
	hits, err := s.qdrant.Search(ctx, vectors[0], req.UserID, req.KnowledgeBaseID, req.TopK)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "检索失败")
	}
	if len(hits) == 0 {
		resp := &ChatResponse{Answer: "根据当前知识库内容无法确认。", Sources: []Source{}}
		logID, _ := s.saveLog(req, resp.Answer, resp.Sources, start, 0, 0)
		resp.ChatLogID = logID
		return resp, nil
	}

	//解析chunkIDs和scoreByChunkID
	chunkIDs := make([]uint64, 0, len(hits))
	scoreByChunkID := make(map[uint64]float64, len(hits))
	for _, hit := range hits {
		chunkIDs = append(chunkIDs, hit.ChunkID)
		scoreByChunkID[hit.ChunkID] = hit.Score
	}

	//查询chunk原文
	chunks, err := s.chunks.FindByIDs(req.UserID, req.KnowledgeBaseID, chunkIDs)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档切片失败")
	}
	chunkByID := make(map[uint64]model.DocumentChunk, len(chunks))
	documentIDs := make([]uint64, 0, len(chunks))
	for _, chunk := range chunks {
		chunkByID[chunk.ID] = chunk
		documentIDs = append(documentIDs, chunk.DocumentID)
	}

	docs, err := s.docs.FindByIDsAndUserID(documentIDs, req.UserID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询文档失败")
	}
	docByID := make(map[uint64]model.Document, len(docs))
	for _, doc := range docs {
		docByID[doc.ID] = doc
	}

	//排序
	ordered := make([]model.DocumentChunk, 0, len(hits))
	for _, hit := range hits {
		if chunk, ok := chunkByID[hit.ChunkID]; ok {
			ordered = append(ordered, chunk)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return scoreByChunkID[ordered[i].ID] > scoreByChunkID[ordered[j].ID]
	})

	//构建提示词
	contextText, sources := buildContextAndSources(ordered, docByID, scoreByChunkID, 6000)
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt()},
		{Role: "user", Content: fmt.Sprintf("知识库上下文：\n%s\n\n用户问题：\n%s", contextText, req.Question)},
	}
	//请求大模型
	chatResult, err := s.chat.Chat(ctx, messages)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "调用 chat 模型失败")
	}
	answer := strings.TrimSpace(chatResult.Answer)
	if answer == "" {
		answer = "根据当前知识库内容无法确认。"
	}

	//保存问答日志
	logID, err := s.saveLog(req, answer, sources, start, chatResult.PromptTokens, chatResult.CompletionTokens)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "保存问答日志失败")
	}

	return &ChatResponse{Answer: answer, Sources: sources, ChatLogID: logID}, nil
}

// ListLogs 查询用户问答日志
func (s *Service) ListLogs(userID uint64) ([]model.ChatLog, error) {
	logs, err := s.logs.ListByUserID(userID, 100)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询问答日志失败")
	}
	return logs, nil
}

func (s *Service) saveLog(req ChatRequest, answer string, sources []Source, start time.Time, promptTokens, completionTokens int) (uint64, error) {
	rawSources, _ := json.Marshal(sources)
	log := model.ChatLog{
		KnowledgeBaseID:  req.KnowledgeBaseID,
		UserID:           req.UserID,
		Question:         req.Question,
		Answer:           answer,
		RetrievedChunks:  datatypes.JSON(rawSources),
		ModelName:        s.chat.ModelName(),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		LatencyMS:        time.Since(start).Milliseconds(),
	}
	if log.PromptTokens == 0 {
		log.PromptTokens = document.EstimateTokens(req.Question + answer)
	}
	if err := s.logs.Create(&log); err != nil {
		return 0, err
	}
	return log.ID, nil
}

func buildContextAndSources(chunks []model.DocumentChunk, docs map[uint64]model.Document, scores map[uint64]float64, maxTokens int) (string, []Source) {
	var builder strings.Builder
	sources := make([]Source, 0, len(chunks))
	usedTokens := 0
	for i, chunk := range chunks {
		tokens := chunk.TokenCount
		if tokens == 0 {
			tokens = document.EstimateTokens(chunk.Content)
		}
		if usedTokens > 0 && usedTokens+tokens > maxTokens {
			break
		}
		doc := docs[chunk.DocumentID]
		builder.WriteString(fmt.Sprintf("[来源 %d: %s / chunk %d]\n%s\n\n", i+1, doc.Filename, chunk.ChunkIndex, chunk.Content))
		usedTokens += tokens

		sources = append(sources, Source{
			DocumentID:     chunk.DocumentID,
			DocumentName:   doc.Filename,
			ChunkID:        chunk.ID,
			ChunkIndex:     chunk.ChunkIndex,
			Score:          scores[chunk.ID],
			ContentPreview: preview(chunk.Content, 160),
		})
	}
	return strings.TrimSpace(builder.String()), sources
}

func preview(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

func systemPrompt() string {
	return `你是企业知识库问答助手。
请只根据给定的知识库上下文回答问题。
如果上下文中没有足够信息，请回答“根据当前知识库内容无法确认”。
不要编造上下文中不存在的信息。
回答后尽量用简洁条目组织。`
}
