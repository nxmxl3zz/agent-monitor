package kiro

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

func TestProvider_BasicMetadata(t *testing.T) {
	p := New()
	if p.ID() != ID {
		t.Errorf("ID = %q, want %q", p.ID(), ID)
	}
	if p.Spec().Auth.Type != core.ProviderAuthTypeLocal {
		t.Errorf("auth type = %v, want local", p.Spec().Auth.Type)
	}
	if p.DashboardWidget().IsZero() {
		t.Error("DashboardWidget is zero")
	}
}

func TestProvider_Fetch_MissingSources(t *testing.T) {
	p := New()
	p.clock = fixedClock{t: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)}
	acct := core.AccountConfig{ID: DefaultAccountID, Provider: ID, Auth: "local"}
	acct.SetPath(PathHintDBKey, filepath.Join(t.TempDir(), "missing.sqlite3"))
	acct.SetPath(PathHintSessionsDirKey, filepath.Join(t.TempDir(), "missing-sessions"))

	snap, err := p.Fetch(context.Background(), acct)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Status != core.StatusUnknown {
		t.Errorf("status = %v, want UNKNOWN", snap.Status)
	}
}

func TestProvider_Fetch_MergesFileAndSQLiteSources(t *testing.T) {
	sessionsDir := t.TempDir()
	headerPath := filepath.Join(sessionsDir, "shared-session.json")
	header := `{
		"session_id": "shared-session",
		"cwd": "/work/file",
		"updated_at": "2026-05-18T09:00:00Z",
		"session_state": {
			"rts_model_state": {"model_info": {"model_id": "claude-sonnet-4-5", "context_window_tokens": 1000}},
			"conversation_metadata": {"user_turn_metadatas": [{"input_tokens": 100, "output_tokens": 50}]}
		}
	}`
	if err := os.WriteFile(headerPath, []byte(header), 0o600); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "shared-session.jsonl"), []byte(`{"kind":"AssistantMessage","data":{"message_id":"m1","content":[],"metadata":{}}}`), 0o600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	dbPath := createKiroDBWithID(t, "shared-session", `{
		"session_id": "shared-session",
		"cwd": "/work/db",
		"updated_at": "2026-05-18T12:00:00Z",
		"session_state": {
			"rts_model_state": {"model_info": {"model_id": "claude-sonnet-4-5"}},
			"conversation_metadata": {"user_turn_metadatas": [{"input_tokens": 999, "output_tokens": 999}]}
		}
	}`)

	p := New()
	p.clock = fixedClock{t: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)}
	acct := core.AccountConfig{ID: DefaultAccountID, Provider: ID, Auth: "local"}
	acct.SetPath(PathHintSessionsDirKey, sessionsDir)
	acct.SetPath(PathHintDBKey, dbPath)

	snap, err := p.Fetch(context.Background(), acct)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Status != core.StatusOK {
		t.Fatalf("status = %v, want OK; msg=%q", snap.Status, snap.Message)
	}

	expectMetric := func(key string, want float64) {
		t.Helper()
		metric, ok := snap.Metrics[key]
		if !ok {
			t.Fatalf("missing metric %s", key)
		}
		if metric.Used == nil || *metric.Used != want {
			got := -1.0
			if metric.Used != nil {
				got = *metric.Used
			}
			t.Fatalf("metric %s = %v, want %v", key, got, want)
		}
	}
	expectMetric("total_conversations", 1)
	expectMetric("total_input_tokens", 100)
	expectMetric("total_output_tokens", 50)

	if len(snap.ModelUsage) != 1 {
		t.Fatalf("len(ModelUsage) = %d, want 1", len(snap.ModelUsage))
	}
	rec := snap.ModelUsage[0]
	if rec.RawSource != "jsonl+sqlite" {
		t.Errorf("RawSource = %q, want jsonl+sqlite", rec.RawSource)
	}
	if rec.Dimensions["workspace"] != "/work/file" {
		t.Errorf("workspace = %q, want /work/file", rec.Dimensions["workspace"])
	}
}
