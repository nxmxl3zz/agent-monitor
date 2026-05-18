package kiro

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestQueryKiroConversations_SQLiteV2(t *testing.T) {
	dbPath := createKiroDB(t, `{
		"session_id": "db-session",
		"cwd": "/work/db",
		"updated_at": "2026-05-18T12:00:00Z",
		"session_state": {
			"rts_model_state": {
				"model_info": {
					"model_id": "amazon.nova-pro",
					"context_window_tokens": 2000
				}
			},
			"conversation_metadata": {
				"user_turn_metadatas": [
					{"input_tokens": 300, "output_tokens": 120}
				]
			}
		},
		"history": [{}, {}]
	}`)

	conversations, err := queryKiroConversations(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("queryKiroConversations: %v", err)
	}
	if len(conversations) != 1 {
		t.Fatalf("len(conversations) = %d, want 1", len(conversations))
	}
	got := conversations[0]
	if got.ConversationID != "db-session" {
		t.Errorf("ConversationID = %q, want db-session", got.ConversationID)
	}
	if got.Source != "sqlite" {
		t.Errorf("Source = %q, want sqlite", got.Source)
	}
	if got.Model != "amazon.nova-pro" {
		t.Errorf("Model = %q, want amazon.nova-pro", got.Model)
	}
	if got.Workspace != "/work/db" {
		t.Errorf("Workspace = %q, want /work/db", got.Workspace)
	}
	if !got.HasMessageCount || got.MessageCount != 2 {
		t.Errorf("MessageCount = %d/%v, want 2/true", got.MessageCount, got.HasMessageCount)
	}
	if !got.HasTokens || got.InputTokens != 300 || got.OutputTokens != 120 || got.TotalTokens != 420 {
		t.Errorf("tokens = in:%d out:%d total:%d has:%v, want 300/120/420/true", got.InputTokens, got.OutputTokens, got.TotalTokens, got.HasTokens)
	}
}

func createKiroDB(t *testing.T, value string) string {
	return createKiroDBWithID(t, "db-session", value)
}

func createKiroDBWithID(t *testing.T, id, value string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "data.sqlite3")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE conversations_v2 (key TEXT, conversation_id TEXT, value TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversations_v2 (key, conversation_id, value) VALUES (?, ?, ?)`, "conversation/"+id, id, value); err != nil {
		t.Fatalf("insert row: %v", err)
	}
	return dbPath
}
