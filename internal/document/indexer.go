package document

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"OurAgent/internal/config"
	"OurAgent/internal/model"
	"OurAgent/internal/repository"
	appsearch "OurAgent/internal/search"
	"OurAgent/internal/storage"
	"OurAgent/internal/vectorstore"

	"github.com/cloudwego/eino/components/embedding"
	"gorm.io/gorm"
)

type Indexer struct {
	db       *gorm.DB
	docs     *repository.DocumentRepository
	qdrant   *vectorstore.QdrantClient
	keyword  appsearch.KeywordStore
	minio    *storage.MinIOClient
	embedder embedding.Embedder
	cfg      *config.Config
}

func NewIndexer(db *gorm.DB, qdrant *vectorstore.QdrantClient, keyword appsearch.KeywordStore, minio *storage.MinIOClient, embedder embedding.Embedder, cfg *config.Config) *Indexer {
	return &Indexer{db: db, docs: repository.NewDocumentRepository(db), qdrant: qdrant, keyword: keyword, minio: minio, embedder: embedder, cfg: cfg}
}

// Index 执行文档解析切片向量化和索引写入
func (i *Indexer) Index(ctx context.Context, documentID uint64, taskID string) error {
	var doc model.Document
	if err := i.db.First(&doc, documentID).Error; err != nil {
		return err
	}

	if doc.Status != model.DocumentStatusProcessing || doc.IndexTaskID != taskID {
		return repository.ErrDocumentIndexLeaseLost
	}

	if err := i.qdrant.DeleteByDocument(ctx, doc.UserID, doc.KnowledgeBaseID, doc.ID); err != nil {
		return i.fail(doc.ID, taskID, "删除旧向量失败", err)
	}
	if i.keyword != nil {
		if err := i.keyword.DeleteByDocumentID(ctx, doc.UserID, doc.ID); err != nil {
			return i.fail(doc.ID, taskID, "删除旧关键词索引失败", err)
		}
	}

	if err := i.db.Where("document_id = ?", doc.ID).Delete(&model.DocumentChildChunk{}).Error; err != nil {
		return i.fail(doc.ID, taskID, "删除旧子chunk失败", err)
	}
	if err := i.db.Where("document_id = ?", doc.ID).Delete(&model.DocumentParentChunk{}).Error; err != nil {
		return i.fail(doc.ID, taskID, "删除旧父chunk失败", err)
	}

	reader, err := i.minio.GetObject(ctx, doc.ObjectKey)
	if err != nil {
		return i.fail(doc.ID, taskID, "读取原始文档失败", err)
	}
	defer reader.Close()

	text, err := ParseReader(ctx, doc.Filename, reader)
	if err != nil {
		return i.fail(doc.ID, taskID, "解析文档失败", err)
	}
	text = NormalizeText(text)
	chunks := SplitDocument(doc.Filename, text, i.cfg.RAG.ChunkSize, i.cfg.RAG.ChunkOverlap)
	if len(chunks) == 0 {
		err := fmt.Errorf("文档没有可索引文本")
		return i.fail(doc.ID, taskID, err.Error(), err)
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
			return i.fail(doc.ID, taskID, "保存父chunk失败", err)
		}

		for childIdx, child := range parent.Children {
			vectors, err := i.embedder.EmbedStrings(ctx, []string{embeddingText(child)})
			if err != nil {
				return i.fail(doc.ID, taskID, "生成embedding失败", err)
			}
			if len(vectors) == 0 || len(vectors[0]) == 0 {
				err := fmt.Errorf("embedding 结果为空")
				return i.fail(doc.ID, taskID, err.Error(), err)
			}
			if err := i.qdrant.EnsureCollection(ctx, len(vectors[0])); err != nil {
				return i.fail(doc.ID, taskID, "初始化向量集合失败", err)
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
				return i.fail(doc.ID, taskID, "保存子chunk失败", err)
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
				return i.fail(doc.ID, taskID, "写入向量库失败", err)
			}

			childChunk.VectorID = strconv.FormatUint(childChunk.ID, 10)
			if err := i.db.Model(&childChunk).Update("vector_id", childChunk.VectorID).Error; err != nil {
				return i.fail(doc.ID, taskID, "更新子chunk vector_id失败", err)
			}
			if i.keyword != nil {
				if err := i.keyword.IndexChild(ctx, childChunk); err != nil {
					return i.fail(doc.ID, taskID, "写入关键词索引失败", err)
				}
			}
			childCount++
		}
	}

	completed, err := i.docs.CompleteDocumentIndex(doc.ID, taskID, childCount)
	if err != nil {
		return err
	}
	if !completed {
		return repository.ErrDocumentIndexLeaseLost
	}
	return nil
}

func embeddingText(chunk Chunk) string {
	if strings.TrimSpace(chunk.SectionPath) == "" {
		return chunk.Content
	}
	return "章节：" + chunk.SectionPath + "\n内容：" + chunk.Content
}

func (i *Indexer) fail(documentID uint64, taskID, message string, cause error) error {
	wrapped := fmt.Errorf("%s: %w", message, cause)
	failed, err := i.docs.FailDocumentIndex(documentID, taskID, wrapped.Error())
	if err != nil {
		return fmt.Errorf("%v，更新失败状态失败: %w", wrapped, err)
	}
	if !failed {
		return repository.ErrDocumentIndexLeaseLost
	}
	return wrapped
}
