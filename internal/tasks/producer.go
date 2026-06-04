package tasks

import (
	"context"
	"time"

	"OurAgent/internal/model"
	"OurAgent/internal/queue"

	"github.com/google/uuid"
)

type Publisher interface {
	PublishJSON(ctx context.Context, routingKey string, value any) error
}

type Producer struct {
	publisher Publisher
}

func NewProducer(publisher Publisher) *Producer {
	return &Producer{publisher: publisher}
}

func (p *Producer) PublishDocumentIndex(ctx context.Context, documentID, userID, knowledgeBaseID uint64) error {
	return p.publisher.PublishJSON(ctx, queue.DocumentIndexRoutingKey, DocumentIndexMessage{
		EventID:         uuid.NewString(),
		Type:            TypeDocumentIndex,
		DocumentID:      documentID,
		UserID:          userID,
		KnowledgeBaseID: knowledgeBaseID,
		Attempt:         0,
		CreatedAt:       time.Now(),
	})
}

func (p *Producer) PublishDocumentDeleteCleanup(ctx context.Context, doc model.Document) error {
	return p.publisher.PublishJSON(ctx, queue.DocumentDeleteRetryRoutingKey, DocumentDeleteCleanupMessage{
		EventID:         uuid.NewString(),
		Type:            TypeDocumentDeleteCleanup,
		DocumentID:      doc.ID,
		UserID:          doc.UserID,
		KnowledgeBaseID: doc.KnowledgeBaseID,
		ObjectKey:       doc.ObjectKey,
		Attempt:         0,
		CreatedAt:       time.Now(),
	})
}
