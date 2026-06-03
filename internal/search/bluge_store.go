package search

import (
	"context"
	"strconv"
	"strings"

	"OurAgent/internal/model"

	"github.com/blugelabs/bluge"
	pkgerrors "github.com/pkg/errors"
)

const (
	fieldChildChunkID    = "child_chunk_id"
	fieldParentChunkID   = "parent_chunk_id"
	fieldDocumentID      = "document_id"
	fieldKnowledgeBaseID = "knowledge_base_id"
	fieldUserID          = "user_id"
	fieldChunkIndex      = "chunk_index"
	fieldSectionPath     = "section_path"
	fieldContent         = "content"
)

type KeywordStore interface {
	IndexChild(ctx context.Context, child model.DocumentChildChunk) error
	DeleteByDocumentID(ctx context.Context, userID, documentID uint64) error
	Search(ctx context.Context, req KeywordSearchRequest) ([]KeywordHit, error)
	Close() error
}

type KeywordSearchRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Query           string
	Limit           int
}

type KeywordHit struct {
	ChildChunkID uint64
	Score        float64
	Rank         int
}

type BlugeStore struct {
	writer *bluge.Writer
}

func NewBlugeStore(path string) (*BlugeStore, error) {
	writer, err := bluge.OpenWriter(bluge.DefaultConfig(path))
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "打开Bluge索引失败")
	}
	return &BlugeStore{writer: writer}, nil
}

// IndexChild 写入或更新子chunk全文索引
func (s *BlugeStore) IndexChild(_ context.Context, child model.DocumentChildChunk) error {
	doc := bluge.NewDocument(blugeChildID(child.ID)).
		AddField(bluge.NewKeywordField(fieldChildChunkID, uintToString(child.ID)).StoreValue()).
		AddField(bluge.NewKeywordField(fieldParentChunkID, uintToString(child.ParentChunkID)).StoreValue()).
		AddField(bluge.NewKeywordField(fieldDocumentID, uintToString(child.DocumentID)).StoreValue()).
		AddField(bluge.NewKeywordField(fieldKnowledgeBaseID, uintToString(child.KnowledgeBaseID)).StoreValue()).
		AddField(bluge.NewKeywordField(fieldUserID, uintToString(child.UserID)).StoreValue()).
		AddField(bluge.NewKeywordField(fieldChunkIndex, strconv.Itoa(child.ChunkIndex)).StoreValue()).
		AddField(bluge.NewTextField(fieldSectionPath, child.SectionPath).StoreValue()).
		AddField(bluge.NewTextField(fieldContent, child.Content).StoreValue())
	if err := s.writer.Update(doc.ID(), doc); err != nil {
		return pkgerrors.WithMessage(err, "写入Bluge索引失败")
	}
	return nil
}

// DeleteByDocumentID 删除文档对应的全文索引
func (s *BlugeStore) DeleteByDocumentID(ctx context.Context, userID, documentID uint64) error {
	reader, err := s.writer.Reader()
	if err != nil {
		return pkgerrors.WithMessage(err, "打开Bluge读取器失败")
	}
	closed := false
	defer func() {
		if !closed {
			_ = reader.Close()
		}
	}()

	query := bluge.NewBooleanQuery().
		AddMust(bluge.NewTermQuery(uintToString(userID)).SetField(fieldUserID)).
		AddMust(bluge.NewTermQuery(uintToString(documentID)).SetField(fieldDocumentID))
	request := bluge.NewTopNSearch(10000, query)
	iterator, err := reader.Search(ctx, request)
	if err != nil {
		return pkgerrors.WithMessage(err, "查询待删除Bluge索引失败")
	}
	childIDs := make([]uint64, 0)
	for {
		match, err := iterator.Next()
		if err != nil {
			return pkgerrors.WithMessage(err, "读取待删除Bluge索引失败")
		}
		if match == nil {
			break
		}
		childID := uint64(0)
		if err := match.VisitStoredFields(func(field string, value []byte) bool {
			if field == fieldChildChunkID {
				childID = stringToUint(string(value))
			}
			return true
		}); err != nil {
			return pkgerrors.WithMessage(err, "读取待删除Bluge字段失败")
		}
		if childID == 0 {
			continue
		}
		childIDs = append(childIDs, childID)
	}
	if err := reader.Close(); err != nil {
		return pkgerrors.WithMessage(err, "关闭Bluge读取器失败")
	}
	closed = true
	for _, childID := range childIDs {
		if err := s.writer.Delete(bluge.Identifier(blugeChildID(childID))); err != nil {
			return pkgerrors.WithMessage(err, "删除Bluge索引失败")
		}
	}
	return nil
}

// Search 执行BM25关键词检索
func (s *BlugeStore) Search(ctx context.Context, req KeywordSearchRequest) ([]KeywordHit, error) {
	queryText := strings.TrimSpace(req.Query)
	if queryText == "" {
		return []KeywordHit{}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	reader, err := s.writer.Reader()
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "打开Bluge读取器失败")
	}
	defer reader.Close()

	contentQuery := bluge.NewMatchQuery(queryText).SetField(fieldContent)
	sectionQuery := bluge.NewMatchQuery(queryText).SetField(fieldSectionPath).SetBoost(1.5)
	textQuery := bluge.NewBooleanQuery().AddShould(contentQuery, sectionQuery).SetMinShould(1)
	query := bluge.NewBooleanQuery().
		AddMust(bluge.NewTermQuery(uintToString(req.UserID)).SetField(fieldUserID)).
		AddMust(bluge.NewTermQuery(uintToString(req.KnowledgeBaseID)).SetField(fieldKnowledgeBaseID)).
		AddMust(textQuery)

	iterator, err := reader.Search(ctx, bluge.NewTopNSearch(limit, query))
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "执行Bluge检索失败")
	}
	hits := make([]KeywordHit, 0, limit)
	for {
		match, err := iterator.Next()
		if err != nil {
			return nil, pkgerrors.WithMessage(err, "读取Bluge检索结果失败")
		}
		if match == nil {
			break
		}
		childID := uint64(0)
		if err := match.VisitStoredFields(func(field string, value []byte) bool {
			if field == fieldChildChunkID {
				childID = stringToUint(string(value))
			}
			return true
		}); err != nil {
			return nil, pkgerrors.WithMessage(err, "读取Bluge检索字段失败")
		}
		if childID == 0 {
			continue
		}
		hits = append(hits, KeywordHit{
			ChildChunkID: childID,
			Score:        match.Score,
			Rank:         len(hits) + 1,
		})
	}
	return hits, nil
}

// Close 关闭Bluge索引写入器
func (s *BlugeStore) Close() error {
	return s.writer.Close()
}

func blugeChildID(id uint64) string {
	return "child_" + uintToString(id)
}

func uintToString(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func stringToUint(value string) uint64 {
	parsed, _ := strconv.ParseUint(value, 10, 64)
	return parsed
}
