package document

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/llm"
	"OurAgent/internal/model"
	"OurAgent/internal/vectorstore"

	"gorm.io/gorm"
)

type Indexer struct {
	db       *gorm.DB
	qdrant   *vectorstore.QdrantClient
	embedder llm.EmbeddingProvider
	cfg      *config.Config
}

func NewIndexer(db *gorm.DB, qdrant *vectorstore.QdrantClient, embedder llm.EmbeddingProvider, cfg *config.Config) *Indexer {
	return &Indexer{db: db, qdrant: qdrant, embedder: embedder, cfg: cfg}
}

// IndexAsync 异步执行文档索引
func (i *Indexer) IndexAsync(documentID uint64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = i.Index(ctx, documentID)
	}()
}

// Index 执行文档解析切片向量化和索引写入
func (i *Indexer) Index(ctx context.Context, documentID uint64) error {
	var doc model.Document
	if err := i.db.First(&doc, documentID).Error; err != nil {
		return err
	}

	if err := i.updateStatus(doc.ID, "processing", "", 0); err != nil {
		return err
	}

	if err := i.qdrant.DeleteByDocument(ctx, doc.UserID, doc.KnowledgeBaseID, doc.ID); err != nil {
		i.fail(doc.ID, fmt.Sprintf("删除旧向量失败: %v", err))
		return err
	}

	if err := i.db.Where("document_id = ?", doc.ID).Delete(&model.DocumentChunk{}).Error; err != nil {
		i.fail(doc.ID, fmt.Sprintf("删除旧 chunk 失败: %v", err))
		return err
	}

	text, err := ParseFile(doc.FilePath)
	if err != nil {
		i.fail(doc.ID, fmt.Sprintf("解析文档失败: %v", err))
		return err
	}
	text = NormalizeText(text)
	chunks := SplitText(text, i.cfg.RAG.ChunkSize, i.cfg.RAG.ChunkOverlap)
	if len(chunks) == 0 {
		err := fmt.Errorf("文档没有可索引文本")
		i.fail(doc.ID, err.Error())
		return err
	}

	chunkCount := 0
	for idx, content := range chunks {
		vectors, err := i.embedder.Embed(ctx, []string{content})
		if err != nil {
			i.fail(doc.ID, fmt.Sprintf("生成 embedding 失败: %v", err))
			return err
		}
		if len(vectors) == 0 || len(vectors[0]) == 0 {
			err := fmt.Errorf("embedding 结果为空")
			i.fail(doc.ID, err.Error())
			return err
		}
		if err := i.qdrant.EnsureCollection(ctx, len(vectors[0])); err != nil {
			i.fail(doc.ID, fmt.Sprintf("初始化向量集合失败: %v", err))
			return err
		}

		chunk := model.DocumentChunk{
			DocumentID:      doc.ID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			UserID:          doc.UserID,
			ChunkIndex:      idx,
			Content:         content,
			TokenCount:      EstimateTokens(content),
		}
		if err := i.db.Create(&chunk).Error; err != nil {
			i.fail(doc.ID, fmt.Sprintf("保存 chunk 失败: %v", err))
			return err
		}

		payload := map[string]interface{}{
			"chunk_id":          chunk.ID,
			"document_id":       doc.ID,
			"knowledge_base_id": doc.KnowledgeBaseID,
			"user_id":           doc.UserID,
			"chunk_index":       idx,
			"document_name":     doc.Filename,
		}
		if err := i.qdrant.Upsert(ctx, chunk.ID, vectors[0], payload); err != nil {
			i.fail(doc.ID, fmt.Sprintf("写入向量库失败: %v", err))
			return err
		}

		chunk.VectorID = strconv.FormatUint(chunk.ID, 10)
		if err := i.db.Model(&chunk).Update("vector_id", chunk.VectorID).Error; err != nil {
			i.fail(doc.ID, fmt.Sprintf("更新 chunk vector_id 失败: %v", err))
			return err
		}
		chunkCount++
	}

	return i.updateStatus(doc.ID, "completed", "", chunkCount)
}

func (i *Indexer) updateStatus(documentID uint64, status, message string, chunkCount int) error {
	updates := map[string]interface{}{
		"status":        status,
		"error_message": message,
	}
	if chunkCount > 0 {
		updates["chunk_count"] = chunkCount
	} else if status == "processing" || status == "failed" {
		updates["chunk_count"] = 0
	}
	return i.db.Model(&model.Document{}).Where("id = ?", documentID).Updates(updates).Error
}

func (i *Indexer) fail(documentID uint64, message string) {
	_ = i.updateStatus(documentID, "failed", message, 0)
}
