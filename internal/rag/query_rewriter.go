package rag

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	pkgerrors "github.com/pkg/errors"
)

const (
	QueryTypeOriginal  = "original"
	QueryTypeRewrite   = "rewrite"
	QueryTypeExpansion = "expansion"
	QueryTypeHyde      = "hyde"
	QueryTypeStepBack  = "step_back"
)

type QueryRewriter interface {
	Rewrite(ctx context.Context, req RewriteRequest) (*RewriteResult, error)
}

type RewriteRequest struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
	MaxQueries      int
	IncludeOriginal bool
}

type RewriteResult struct {
	OriginalQuery string
	Queries       []RewrittenQuery
}

type RewrittenQuery struct {
	Query  string
	Type   string
	Reason string
}

type FallbackQueryRewriter struct{}

func NewFallbackQueryRewriter() *FallbackQueryRewriter {
	return &FallbackQueryRewriter{}
}

// Rewrite返回原始问题作为检索query
func (r *FallbackQueryRewriter) Rewrite(_ context.Context, req RewriteRequest) (*RewriteResult, error) {
	query := strings.TrimSpace(req.Question)
	return &RewriteResult{
		OriginalQuery: query,
		Queries: []RewrittenQuery{
			{Query: query, Type: QueryTypeOriginal},
		},
	}, nil
}

type LLMQueryRewriter struct {
	chat einomodel.BaseChatModel
}

func NewLLMQueryRewriter(chat einomodel.BaseChatModel) *LLMQueryRewriter {
	return &LLMQueryRewriter{chat: chat}
}

// Rewrite调用ChatModel生成更适合检索的query
func (r *LLMQueryRewriter) Rewrite(ctx context.Context, req RewriteRequest) (*RewriteResult, error) {
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return &RewriteResult{OriginalQuery: question, Queries: []RewrittenQuery{}}, nil
	}
	maxQueries := req.MaxQueries
	if maxQueries <= 0 {
		maxQueries = 3
	}
	messages := []*schema.Message{
		schema.SystemMessage(queryRewriteSystemPrompt),
		schema.UserMessage(queryRewriteUserPrompt(question, maxQueries)),
	}
	resp, err := r.chat.Generate(ctx, messages)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "调用查询改写模型失败")
	}
	queries, err := parseRewriteQueries(resp.Content)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "解析查询改写结果失败")
	}
	result := &RewriteResult{OriginalQuery: question, Queries: []RewrittenQuery{}}
	//需要时保留原始问题
	if req.IncludeOriginal {
		result.Queries = append(result.Queries, RewrittenQuery{Query: question, Type: QueryTypeOriginal})
	}
	for _, item := range queries {
		item.Query = strings.TrimSpace(item.Query)
		item.Type = strings.TrimSpace(item.Type)
		if item.Query == "" {
			continue
		}
		if item.Type == "" {
			item.Type = QueryTypeRewrite
		}
		result.Queries = append(result.Queries, item)
		if len(result.Queries) >= maxQueries {
			break
		}
	}
	result.Queries = dedupeRewrittenQueries(result.Queries)
	if len(result.Queries) == 0 {
		result.Queries = append(result.Queries, RewrittenQuery{Query: question, Type: QueryTypeOriginal})
	}
	return result, nil
}

const queryRewriteSystemPrompt = `你是RAG检索查询优化器。
你的任务是把用户问题改写成更适合知识库检索的query。
要求：
1. 保留用户原始意图
2. 不要回答问题
3. 不要编造具体业务事实
4. 可以把口语化表达改成书面语
5. 可以补全明显指代不清的业务对象
6. 至少生成1个rewrite类型query，用于改写原问题
7. 如果问题适合扩展，生成1到2个expansion类型query，用于覆盖同义表达、相关流程、原因、影响或处理方式
8. expansion不能偏离原问题意图，不能扩展成过宽的背景问题
9. 只输出JSON，不要输出Markdown`

func queryRewriteUserPrompt(question string, maxQueries int) string {
	return strings.TrimSpace(`
用户问题：
` + question + `

请生成最多` + strconv.Itoa(maxQueries) + `个检索query。
query类型说明：
- rewrite：把原问题改写成清晰、书面、指代明确的检索问题
- expansion：从同义表达、相关流程、原因、影响、处理方式等角度扩展检索问题

输出格式：
{
  "queries": [
    {
      "query": "改写后的检索问题",
      "type": "rewrite",
      "reason": "改写原因"
    }
  ]
}

type只能使用rewrite或expansion。`)
}

type rewriteJSON struct {
	Queries []RewrittenQuery `json:"queries"`
}

func parseRewriteQueries(content string) ([]RewrittenQuery, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		content = content[start : end+1]
	}
	var parsed rewriteJSON
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, err
	}
	return parsed.Queries, nil
}

func dedupeRewrittenQueries(queries []RewrittenQuery) []RewrittenQuery {
	seen := make(map[string]struct{}, len(queries))
	result := make([]RewrittenQuery, 0, len(queries))
	for _, item := range queries {
		key := strings.TrimSpace(item.Query)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		item.Query = key
		result = append(result, item)
	}
	return result
}
