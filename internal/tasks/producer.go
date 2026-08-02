package tasks

import (
	"context"
	"time"

	"OurAgent/internal/model"
	"OurAgent/internal/queue"
	"OurAgent/internal/repository"

	"github.com/google/uuid"
)

type Publisher interface {
	PublishJSON(ctx context.Context, routingKey string, value any) error
}

func (p *Producer) PublishConversationCompact(ctx context.Context, task repository.ConversationCompactionTask) error {
	return p.publisher.PublishJSON(ctx, queue.ConversationCompactRoutingKey, ConversationCompactMessage{
		EventID:            task.TaskID,
		Type:               TypeConversationCompact,
		ConversationID:     task.ConversationID,
		UserID:             task.UserID,
		KnowledgeBaseID:    task.KnowledgeBaseID,
		SnapshotLastLogID:  task.SnapshotLastLogID,
		BaseSummaryVersion: task.BaseSummaryVersion,
		Attempt:            task.Attempt,
		CreatedAt:          time.Now(),
	})
}

type Producer struct {
	publisher Publisher
}

func NewProducer(publisher Publisher) *Producer {
	return &Producer{publisher: publisher}
}

func (p *Producer) PublishDocumentIndex(ctx context.Context, documentID, userID, knowledgeBaseID uint64) error {
	return p.PublishExternalDocumentIndex(ctx, documentID, userID, knowledgeBaseID, 0)
}

func (p *Producer) PublishExternalDocumentIndex(ctx context.Context, documentID, userID, knowledgeBaseID, externalDocumentID uint64) error {
	return p.publisher.PublishJSON(ctx, queue.DocumentIndexRoutingKey, DocumentIndexMessage{
		EventID:            uuid.NewString(),
		Type:               TypeDocumentIndex,
		DocumentID:         documentID,
		UserID:             userID,
		KnowledgeBaseID:    knowledgeBaseID,
		ExternalDocumentID: externalDocumentID,
		Attempt:            0,
		CreatedAt:          time.Now(),
	})
}

func (p *Producer) PublishDocumentDeleteCleanup(ctx context.Context, doc model.Document) error {
	return p.publishDocumentCleanup(ctx, doc, 0, DeleteModeDelete)
}

func (p *Producer) PublishExternalDocumentDeindex(ctx context.Context, doc model.Document, externalDocumentID uint64) error {
	return p.publishDocumentCleanup(ctx, doc, externalDocumentID, DeleteModeDeindex)
}

func (p *Producer) PublishExternalDocumentDelete(ctx context.Context, doc model.Document, externalDocumentID uint64) error {
	return p.publishDocumentCleanup(ctx, doc, externalDocumentID, DeleteModeDelete)
}

func (p *Producer) publishDocumentCleanup(ctx context.Context, doc model.Document, externalDocumentID uint64, mode string) error {
	return p.publisher.PublishJSON(ctx, queue.DocumentDeleteRoutingKey, DocumentDeleteCleanupMessage{
		EventID:            uuid.NewString(),
		Type:               TypeDocumentDeleteCleanup,
		DocumentID:         doc.ID,
		UserID:             doc.UserID,
		KnowledgeBaseID:    doc.KnowledgeBaseID,
		ObjectKey:          doc.ObjectKey,
		ExternalDocumentID: externalDocumentID,
		Mode:               mode,
		Attempt:            0,
		CreatedAt:          time.Now(),
	})
}

func (p *Producer) PublishSourceSync(ctx context.Context, sourceID, userID, knowledgeBaseID uint64, taskID string, attempt int) error {
	if taskID == "" {
		taskID = uuid.NewString()
	}
	return p.publisher.PublishJSON(ctx, queue.SourceSyncRoutingKey, SourceSyncMessage{
		EventID:         taskID,
		Type:            TypeSourceSync,
		SourceID:        sourceID,
		UserID:          userID,
		KnowledgeBaseID: knowledgeBaseID,
		Attempt:         attempt,
		CreatedAt:       time.Now(),
	})
}
