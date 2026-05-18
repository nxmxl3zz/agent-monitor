package copilot

import (
	"path/filepath"
	"testing"

	"github.com/janekbaraniewski/openusage/internal/providers/shared"
)

// TestParseCopilotTelemetrySessionFile_OTelToolDedup verifies that when the
// same tool_call_id surfaces across request, start (metric), and complete
// (response) records, only the highest-priority record is emitted.
func TestParseCopilotTelemetrySessionFile_OTelToolDedup(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "session-otel-dedup"
	eventsPath := filepath.Join(tmpDir, "events.jsonl")

	events := []map[string]any{
		{
			"type":      "session.start",
			"timestamp": "2026-03-01T10:00:00Z",
			"data": map[string]any{
				"sessionId": sessionID,
				"context": map[string]any{
					"cwd":        "/Users/test/openusage",
					"repository": "janekbaraniewski/openusage",
				},
			},
		},
		{
			"type":      "session.model_change",
			"timestamp": "2026-03-01T10:00:01Z",
			"data":      map[string]any{"newModel": "claude-sonnet-4.5"},
		},
		// Request record (priority: request)
		{
			"type":      "assistant.message",
			"timestamp": "2026-03-01T10:00:02Z",
			"id":        "assistant-msg-1",
			"data": map[string]any{
				"messageId": "msg-1",
				"toolRequests": []map[string]any{
					{
						"toolCallId": "call-shared-1",
						"name":       "edit",
						"arguments":  map[string]any{"filePath": "foo.go"},
					},
				},
			},
		},
		// Start record (priority: metric)
		{
			"type":      "tool.execution_start",
			"timestamp": "2026-03-01T10:00:03Z",
			"data": map[string]any{
				"toolCallId": "call-shared-1",
				"toolName":   "edit",
				"arguments":  map[string]any{"filePath": "foo.go"},
			},
		},
		// Complete record (priority: response — should win)
		{
			"type":      "tool.execution_complete",
			"timestamp": "2026-03-01T10:00:04Z",
			"data": map[string]any{
				"toolCallId": "call-shared-1",
				"success":    true,
				"result":     map[string]any{"content": "ok"},
			},
		},
	}
	writeCopilotTelemetryEvents(t, eventsPath, events)

	out, err := parseCopilotTelemetrySessionFile(eventsPath, sessionID)
	if err != nil {
		t.Fatalf("parseCopilotTelemetrySessionFile() error: %v", err)
	}

	// Count tool-usage events for the shared tool_call_id.
	var matches []shared.TelemetryEvent
	for _, ev := range out {
		if ev.EventType == shared.TelemetryEventTypeToolUsage && ev.ToolCallID == "call-shared-1" {
			matches = append(matches, ev)
		}
	}
	if len(matches) != 1 {
		for i, ev := range matches {
			t.Logf("match[%d]: event=%v status=%v", i, ev.Payload["event"], ev.Status)
		}
		t.Fatalf("expected exactly 1 dedup'd tool-usage event for call-shared-1, got %d", len(matches))
	}
	got := matches[0]
	if eventTag, _ := got.Payload["event"].(string); eventTag != "tool.execution_complete" {
		t.Fatalf("winner event = %q, want tool.execution_complete (response priority)", eventTag)
	}
	if got.Status != shared.TelemetryStatusOK {
		t.Fatalf("winner status = %v, want OK (from response)", got.Status)
	}
}

// TestDedupCopilotOTelToolEvents_NoToolCallID confirms that events lacking a
// ToolCallID are not collapsed.
func TestDedupCopilotOTelToolEvents_NoToolCallID(t *testing.T) {
	in := []shared.TelemetryEvent{
		{
			EventType: shared.TelemetryEventTypeToolUsage,
			ToolName:  "edit",
			Payload:   map[string]any{"event": "assistant.message.tool_request"},
		},
		{
			EventType: shared.TelemetryEventTypeToolUsage,
			ToolName:  "edit",
			Payload:   map[string]any{"event": "tool.execution_complete"},
		},
	}
	out := dedupCopilotOTelToolEvents(in)
	if len(out) != 2 {
		t.Fatalf("expected both events to pass through (no tool_call_id), got %d", len(out))
	}
}
