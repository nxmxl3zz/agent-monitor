package kiro

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseKiroSession_AggregatesJSONLAndHeaderMetadata(t *testing.T) {
	dir := t.TempDir()
	headerPath := filepath.Join(dir, "session-1.json")
	header := `{
		"session_id": "session-1",
		"cwd": "/work/openusage",
		"updated_at": "2026-05-18T09:00:00Z",
		"session_state": {
			"rts_model_state": {
				"model_info": {
					"model_id": "claude-sonnet-4-5",
					"context_window_tokens": 1000
				}
			},
			"conversation_metadata": {
				"user_turn_metadatas": [
					{
						"input_tokens": 100,
						"output_tokens": 50,
						"request_end_time": "2026-05-18T10:00:00Z"
					},
					{
						"context_usage_percentage": 0.2,
						"request_end_time": "2026-05-18T11:00:00Z"
					}
				]
			}
		}
	}`
	if err := os.WriteFile(headerPath, []byte(header), 0o600); err != nil {
		t.Fatalf("write header: %v", err)
	}

	jsonl := `{"kind":"AssistantMessage","data":{"message_id":"m1","content":[{"text":"ignored"}],"metadata":{}}}
{"kind":"AssistantMessage","data":{"message_id":"m2","content":[{"text":"abcdefgh"}],"metadata":{"response_size":8}}}`
	if err := os.WriteFile(filepath.Join(dir, "session-1.jsonl"), []byte(jsonl), 0o600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	conv, err := parseKiroSession(headerPath)
	if err != nil {
		t.Fatalf("parseKiroSession: %v", err)
	}
	if conv == nil {
		t.Fatal("parseKiroSession returned nil")
	}
	if conv.ConversationID != "session-1" {
		t.Errorf("ConversationID = %q, want session-1", conv.ConversationID)
	}
	if conv.Workspace != "/work/openusage" {
		t.Errorf("Workspace = %q, want /work/openusage", conv.Workspace)
	}
	if conv.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q, want claude-sonnet-4-5", conv.Model)
	}
	if !conv.HasMessageCount || conv.MessageCount != 2 {
		t.Errorf("MessageCount = %d/%v, want 2/true", conv.MessageCount, conv.HasMessageCount)
	}
	if !conv.HasTokens {
		t.Fatal("HasTokens = false, want true")
	}
	// First assistant uses explicit turn metadata: 100 + 50.
	// Second estimates input from 0.2 * 1000 and output from response_size/4.
	if conv.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", conv.InputTokens)
	}
	if conv.OutputTokens != 52 {
		t.Errorf("OutputTokens = %d, want 52", conv.OutputTokens)
	}
	if conv.TotalTokens != 352 {
		t.Errorf("TotalTokens = %d, want 352", conv.TotalTokens)
	}
}

func TestParseKiroSessionJSONL_DeduplicatesMessageIDLastWins(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "session.jsonl")
	jsonl := `{"kind":"AssistantMessage","data":{"message_id":"m1","content":[{"text":"abcd"}],"metadata":{"response_size":4}}}
{"kind":"AssistantMessage","data":{"message_id":"m1","content":[{"text":"abcdefghijkl"}],"metadata":{"response_size":12}}}`
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	events, err := parseKiroSessionJSONL(jsonlPath, &kiroHeader{})
	if err != nil {
		t.Fatalf("parseKiroSessionJSONL: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].OutputTokens != 3 {
		t.Errorf("OutputTokens = %d, want 3", events[0].OutputTokens)
	}
}
