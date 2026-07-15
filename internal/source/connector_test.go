package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"OurAgent/internal/model"

	"gorm.io/datatypes"
)

func TestNotionListReadsAllDatabasePages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/databases/database-1/query" {
			http.NotFound(w, r)
			return
		}
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"results":[{"id":"page-1","url":"https://notion/page-1","last_edited_time":"2026-07-15T10:00:00Z"}],"has_more":true,"next_cursor":"cursor-2"}`))
			return
		}
		if body["start_cursor"] != "cursor-2" {
			t.Fatalf("unexpected cursor: %v", body["start_cursor"])
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"page-2","url":"https://notion/page-2","last_edited_time":"2026-07-15T11:00:00Z"}],"has_more":false,"next_cursor":""}`))
	}))
	defer server.Close()

	connector := NewNotionConnector()
	connector.client = server.Client()
	configJSON, _ := json.Marshal(map[string]any{"base_url": server.URL, "database_id": "database-1", "page_size": 1})
	credentialJSON, _ := json.Marshal(map[string]any{"access_token": "token"})
	result, err := connector.List(context.Background(), ListRequest{
		Config:     datatypes.JSON(configJSON),
		Credential: datatypes.JSON(credentialJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(result.Items) != 2 {
		t.Fatalf("requests=%d items=%d", requests, len(result.Items))
	}
	if result.Items[0].RemoteID != "page-1" || result.Items[1].RemoteID != "page-2" {
		t.Fatalf("unexpected items: %+v", result.Items)
	}
}

func TestShouldFetchRecoveryStates(t *testing.T) {
	updatedAt := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	item := RemoteItem{RemoteID: "doc-1", UpdatedAt: updatedAt, ContentHash: "same"}
	for _, status := range []string{
		model.ExternalDocumentStatusChanged,
		model.ExternalDocumentStatusFailed,
		model.ExternalDocumentStatusMissing,
		model.ExternalDocumentStatusDeleted,
	} {
		local := model.ExternalDocument{DocumentID: 1, ContentHash: "same", RemoteUpdatedAt: &updatedAt, SyncStatus: status}
		if !shouldFetch(local, item) {
			t.Fatalf("status %s should be fetched", status)
		}
	}
	local := model.ExternalDocument{DocumentID: 1, ContentHash: "same", RemoteUpdatedAt: &updatedAt, SyncStatus: model.ExternalDocumentStatusSynced}
	if shouldFetch(local, item) {
		t.Fatal("unchanged synced document should be skipped")
	}
}
