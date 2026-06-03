package document

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	appsearch "OurAgent/internal/search"
	"OurAgent/internal/vectorstore"

	"github.com/cloudwego/eino/components/embedding"
	"gorm.io/gorm"
)

type Indexer struct {
	db       *gorm.DB
	qdrant   *vectorstore.QdrantClient
	keyword  appsearch.KeywordStore
	embedder embedding.Embedder
	cfg      *config.Config
}

func NewIndexer(db *gorm.DB, qdrant *vectorstore.QdrantClient, keyword appsearch.KeywordStore, embedder embedding.Embedder, cfg *config.Config) *Indexer {
	return &Indexer{db: db, qdrant: qdrant, keyword: keyword, embedder: embedder, cfg: cfg}
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
	if i.keyword != nil {
		if err := i.keyword.DeleteByDocumentID(ctx, doc.UserID, doc.ID); err != nil {
			i.fail(doc.ID, fmt.Sprintf("删除旧关键词索引失败: %v", err))
			return err
		}
	}

	if err := i.db.Where("document_id = ?", doc.ID).Delete(&model.DocumentChildChunk{}).Error; err != nil {
		i.fail(doc.ID, fmt.Sprintf("删除旧子 chunk 失败: %v", err))
		return err
	}
	if err := i.db.Where("document_id = ?", doc.ID).Delete(&model.DocumentParentChunk{}).Error; err != nil {
		i.fail(doc.ID, fmt.Sprintf("删除旧父 chunk 失败: %v", err))
		return err
	}

	text, err := ParseFile(doc.FilePath)
	if err != nil {
		i.fail(doc.ID, fmt.Sprintf("解析文档失败: %v", err))
		return err
	}
	text = NormalizeText(text)
	chunks := SplitDocument(doc.Filename, text, i.cfg.RAG.ChunkSize, i.cfg.RAG.ChunkOverlap)
	if len(chunks) == 0 {
		err := fmt.Errorf("文档没有可索引文本")
		i.fail(doc.ID, err.Error())
		return err
	}

	childCount := 0
	for parentIdx, parent := range chunks {
		parentChunk := model.DocumentParentChunk{
			DocumentID:      doc.ID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			UserID:          doc.UserID,
			ChunkIndex:      parentIdx,
			SectionPath:     parent.SectionPath,
			Content:         parent.Content,
			TokenCount:      parent.TokenCount,
		}
		if err := i.db.Create(&parentChunk).Error; err != nil {
			i.fail(doc.ID, fmt.Sprintf("保存父 chunk 失败: %v", err))
			return err
		}

		for childIdx, child := range parent.Children {
			vectors, err := i.embedder.EmbedStrings(ctx, []string{embeddingText(child)})
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

			childChunk := model.DocumentChildChunk{
				DocumentID:      doc.ID,
				KnowledgeBaseID: doc.KnowledgeBaseID,
				UserID:          doc.UserID,
				ParentChunkID:   parentChunk.ID,
				ChunkIndex:      childIdx,
				SectionPath:     child.SectionPath,
				Content:         child.Content,
				TokenCount:      child.TokenCount,
			}
			if err := i.db.Create(&childChunk).Error; err != nil {
				i.fail(doc.ID, fmt.Sprintf("保存子 chunk 失败: %v", err))
				return err
			}

			payload := map[string]interface{}{
				"chunk_id":          childChunk.ID,
				"parent_chunk_id":   parentChunk.ID,
				"chunk_type":        "child",
				"document_id":       doc.ID,
				"knowledge_base_id": doc.KnowledgeBaseID,
				"user_id":           doc.UserID,
				"chunk_index":       childIdx,
				"document_name":     doc.Filename,
				"section_path":      child.SectionPath,
			}
			if err := i.qdrant.Upsert(ctx, childChunk.ID, vectors[0], payload); err != nil {
				i.fail(doc.ID, fmt.Sprintf("写入向量库失败: %v", err))
				return err
			}

			childChunk.VectorID = strconv.FormatUint(childChunk.ID, 10)
			if err := i.db.Model(&childChunk).Update("vector_id", childChunk.VectorID).Error; err != nil {
				i.fail(doc.ID, fmt.Sprintf("更新子 chunk vector_id 失败: %v", err))
				return err
			}
			if i.keyword != nil {
				if err := i.keyword.IndexChild(ctx, childChunk); err != nil {
					i.fail(doc.ID, fmt.Sprintf("写入关键词索引失败: %v", err))
					return err
				}
			}
			childCount++
		}
	}

	return i.updateStatus(doc.ID, "completed", "", childCount)
}

func embeddingText(chunk Chunk) string {
	if strings.TrimSpace(chunk.SectionPath) == "" {
		return chunk.Content
	}
	return "章节：" + chunk.SectionPath + "\n内容：" + chunk.Content
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
