package websearch

import "context"

type Answerer interface {
	Answer(ctx context.Context, req Request) (*Result, error)
}

type Request struct {
	UserID          uint64
	KnowledgeBaseID uint64
	Question        string
}

type Result struct {
	Answer  string
	Sources []Source
}

type Source struct {
	Title   string
	URL     string
	Snippet string
}
