package tasks

import (
	"context"
	"testing"

	"OurAgent/internal/model"
	"OurAgent/internal/queue"
)

type capturePublisher struct {
	routingKey string
	value      any
}

func (c *capturePublisher) PublishJSON(_ context.Context, routingKey string, value any) error {
	c.routingKey = routingKey
	c.value = value
	return nil
}

func TestProducerPublishesDeindexToMainQueue(t *testing.T) {
	capture := &capturePublisher{}
	producer := NewProducer(capture)
	doc := model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, ObjectKey: "doc.md"}
	if err := producer.PublishExternalDocumentDeindex(context.Background(), doc, 4); err != nil {
		t.Fatal(err)
	}
	if capture.routingKey != queue.DocumentDeleteRoutingKey {
		t.Fatalf("unexpected routing key: %s", capture.routingKey)
	}
	msg, ok := capture.value.(DocumentDeleteCleanupMessage)
	if !ok {
		t.Fatalf("unexpected message type: %T", capture.value)
	}
	if msg.Mode != DeleteModeDeindex || msg.ExternalDocumentID != 4 {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestProducerPublishesDeleteToMainQueue(t *testing.T) {
	capture := &capturePublisher{}
	producer := NewProducer(capture)
	doc := model.Document{ID: 1, UserID: 2, KnowledgeBaseID: 3, ObjectKey: "doc.md"}
	if err := producer.PublishExternalDocumentDelete(context.Background(), doc, 4); err != nil {
		t.Fatal(err)
	}
	msg := capture.value.(DocumentDeleteCleanupMessage)
	if capture.routingKey != queue.DocumentDeleteRoutingKey || msg.Mode != DeleteModeDelete {
		t.Fatalf("routing=%s message=%+v", capture.routingKey, msg)
	}
}

func TestProducerKeepsSourceTaskIdentity(t *testing.T) {
	capture := &capturePublisher{}
	producer := NewProducer(capture)
	if err := producer.PublishSourceSync(context.Background(), 1, 2, 3, "task-1", 2); err != nil {
		t.Fatal(err)
	}
	msg := capture.value.(SourceSyncMessage)
	if capture.routingKey != queue.SourceSyncRoutingKey || msg.EventID != "task-1" || msg.Attempt != 2 {
		t.Fatalf("routing=%s message=%+v", capture.routingKey, msg)
	}
}
