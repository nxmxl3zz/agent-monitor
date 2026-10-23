package telemetry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

func TestSessionStateTransitions(t *testing.T) {
	cases := []struct {
		name, provider, lines string
		state                 core.SessionState
		active                string
	}{
		{"codex begins", "codex", `{"type":"event_msg","payload":{"type":"task_started"}}`, core.SessionWorking, ""},
		{"codex finishes", "codex", `{"type":"event_msg","payload":{"type":"task_started"}}
{"type":"event_msg","payload":{"type":"task_complete"}}`, core.SessionFinished, ""},
		{"codex aborts", "codex", `{"type":"event_msg","payload":{"type":"turn_aborted"}}`, core.SessionInterrupted, ""},
		{"codex asks", "codex", `{"type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"request_user_input"}}`, core.SessionWaiting, "request_user_input"},
		{"parallel tools", "codex", `{"type":"response_item","payload":{"type":"function_call","call_id":"a","name":"Read"}}
{"type":"response_item","payload":{"type":"function_call","call_id":"b","name":"Bash"}}
{"type":"response_item","payload":{"type":"function_call_output","call_id":"a"}}`, core.SessionWorking, "Bash"},
		{"claude request starts", "claude_code", `{"type":"api-request"}`, core.SessionWorking, ""},
		{"claude tools", "claude_code", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r","name":"Read"}],"stop_reason":"tool_use"}}`, core.SessionWorking, "Read"},
		{"claude finishes", "claude_code", `{"type":"assistant","message":{"stop_reason":"end_turn"}}`, core.SessionFinished, ""},
		{"claude asks", "claude_code", `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"q","name":"AskUserQuestion"}],"stop_reason":"tool_use"}}`, core.SessionWaiting, "AskUserQuestion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := &sessionFile{row: core.SessionActivity{ProviderID: tc.provider, State: core.SessionUnknown}, pending: make(map[string]string)}
			for i, line := range strings.Split(tc.lines, "\n") {
				line = strings.Replace(line, `{`, fmt.Sprintf(`{"timestamp":"2026-10-22T18:00:%02dZ",`, i), 1)
				file.apply([]byte(line))
			}
			if file.row.State != tc.state || file.row.ActiveTool != tc.active {
				t.Fatalf("got state=%s tool=%q; want %s %q", file.row.State, file.row.ActiveTool, tc.state, tc.active)
			}
		})
	}
}

func TestSessionTrackerAppendPartialStaleAndReplace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := `{"type":"session_meta","payload":{"id":"s","cwd":"/project","source":"vscode"}}` + "\n"
	start := `{"timestamp":"2026-10-22T18:00:00Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n"
	finish := `{"timestamp":"2026-10-22T18:00:01Z","type":"event_msg","payload":{"type":"task_complete"}}`
	write(base + start + finish[:30])
	tracker := NewSessionTracker()
	now := time.Date(2026, 10, 22, 18, 0, 2, 0, time.UTC)
	roots := []SessionRoot{{"codex", dir}}
	rows, warnings := tracker.Snapshot(ctx, roots, now, time.Time{})
	if len(warnings) > 0 || len(rows) != 1 || rows[0].State != core.SessionWorking {
		t.Fatalf("partial: %+v %v", rows, warnings)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(finish[30:] + "\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = tracker.Snapshot(ctx, roots, now, time.Time{})
	if rows[0].State != core.SessionFinished {
		t.Fatalf("finish: %+v", rows[0])
	}
	// An older event cannot resurrect a completed turn.
	file, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = file.WriteString(start)
	file.Close()
	rows, _ = tracker.Snapshot(ctx, roots, now, time.Time{})
	if rows[0].State != core.SessionFinished {
		t.Fatal("out-of-order start resurrected a turn")
	}
	write(base + start)
	rows, _ = tracker.Snapshot(ctx, roots, now.Add(SessionEvidenceTTL), time.Time{})
	if rows[0].State != core.SessionUnknown || rows[0].ActiveTool != "" {
		t.Fatal("stale state presented as working")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rows, _ = tracker.Snapshot(ctx, roots, now, time.Time{})
	if len(rows) != 0 {
		t.Fatal("deleted file retained")
	}
}

func TestSessionTokensUseCanonicalWinners(t *testing.T) {
	_, db, store := openUsageViewRawTestStore(t)
	now := time.Now().UTC()
	request := IngestRequest{SourceSystem: SourceSystem("claude_code"), SourceChannel: SourceChannelJSONL, OccurredAt: now, ProviderID: "anthropic", AgentName: "claude_code", AccountID: "claude_code", SessionID: "s", MessageID: "m", EventType: EventTypeMessageUsage, TokenUsage: core.TokenUsage{TotalTokens: int64Ptr(123), Requests: int64Ptr(1)}}
	if _, err := store.Ingest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.SourceChannel = SourceChannelHook
	if _, err := store.Ingest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	rows := []core.SessionActivity{{ProviderID: "claude_code", SessionID: "s"}, {ProviderID: "codex", SessionID: "missing"}}
	if err := ApplySessionTokens(context.Background(), db, rows, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !rows[0].TokensKnown || rows[0].TotalTokens != 123 {
		t.Fatalf("duplicates counted: %+v", rows[0])
	}
	if rows[1].TokensKnown {
		t.Fatal("missing usage became a measured zero")
	}
}
