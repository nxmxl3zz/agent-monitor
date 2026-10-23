package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/janekbaraniewski/openusage/internal/config"
	"github.com/janekbaraniewski/openusage/internal/core"
	"github.com/janekbaraniewski/openusage/internal/telemetry"
)

func TestSessionRootsRespectProviderSelectionAndPaths(t *testing.T) {
	roots := sessionRoots([]core.AccountConfig{{Provider: "codex", ProviderPaths: map[string]string{"config_dir": "/custom/codex"}}, {Provider: "deepseek"}})
	if len(roots) != 1 || roots[0].Path != filepath.Join("/custom/codex", "sessions") || roots[0].ProviderID != "codex" {
		t.Fatalf("unexpected roots: %+v", roots)
	}
}

func TestSessionsEndpointReadsActivityWithoutExposingContent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENUSAGE_CONFIG_DIR", filepath.Join(dir, "profile"))
	cfg := config.DefaultConfig()
	cfg.AutoDetect = false
	cfg.Accounts = []core.AccountConfig{{ID: "codex", Provider: "codex", ProviderPaths: map[string]string{"sessions_dir": dir}}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	content := `{"type":"session_meta","payload":{"id":"s","cwd":"/work","source":"vscode"}}` + "\n" +
		`{"timestamp":"` + stamp + `","type":"event_msg","payload":{"type":"task_started","prompt":"SENSITIVE_CONTENT_MUST_NOT_LEAK"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := telemetry.OpenStore(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RunMigrations(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: store, sessionTracker: telemetry.NewSessionTracker()}
	w := httptest.NewRecorder()
	svc.handleSessions(w, httptest.NewRequest(http.MethodGet, "/v1/sessions?window=1d", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SENSITIVE_CONTENT") {
		t.Fatal("transcript content exposed")
	}
	var frame core.SessionActivityFrame
	if err := json.Unmarshal(w.Body.Bytes(), &frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.Sessions) != 1 || frame.Sessions[0].State != core.SessionWorking {
		t.Fatalf("frame=%+v", frame)
	}
}

func TestSessionsEndpointRejectsMutations(t *testing.T) {
	svc := &Service{}
	w := httptest.NewRecorder()
	svc.handleSessions(w, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", w.Code)
	}
}
