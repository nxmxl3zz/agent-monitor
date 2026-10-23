package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/janekbaraniewski/openusage/internal/core"
	"github.com/janekbaraniewski/openusage/internal/telemetry"
)

func sessionRoots(accounts []core.AccountConfig) []telemetry.SessionRoot {
	home, _ := os.UserHomeDir()
	var roots []telemetry.SessionRoot
	seen := make(map[string]bool)
	for _, acct := range accounts {
		var paths []string
		switch acct.Provider {
		case "codex":
			dir := os.Getenv("CODEX_HOME")
			if dir == "" {
				dir = filepath.Join(home, ".codex")
			}
			dir = acct.Path("config_dir", dir)
			paths = []string{acct.Path("sessions_dir", filepath.Join(dir, "sessions"))}
		case "claude_code":
			paths = []string{acct.Path("projects_dir", filepath.Join(home, ".claude", "projects")), filepath.Join(home, ".config", "claude", "projects")}
		}
		for _, path := range paths {
			if strings.HasPrefix(path, "~/") {
				path = filepath.Join(home, path[2:])
			}
			key := acct.Provider + ":" + path
			if !seen[key] {
				roots = append(roots, telemetry.SessionRoot{ProviderID: acct.Provider, Path: path})
				seen[key] = true
			}
		}
	}
	return roots
}

func (s *Service) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	window := core.ParseTimeWindow(r.URL.Query().Get("window"))
	accounts, _, err := LoadAccountsAndNorm()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session configuration unavailable")
		return
	}
	if s.sessionTracker == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "session tracker unavailable")
		return
	}
	now := s.now()
	rows, warnings := s.sessionTracker.Snapshot(r.Context(), sessionRoots(accounts), now, window.Since())
	if err := telemetry.ApplySessionTokens(r.Context(), s.store.DB(), rows, window.Since()); err != nil {
		warnings = append(warnings, "per-session usage temporarily unavailable")
	}
	writeJSON(w, http.StatusOK, core.SessionActivityFrame{Sessions: rows, UpdatedAt: now, Window: window, Warnings: warnings})
}

func (c *Client) Sessions(ctx context.Context, window core.TimeWindow) (core.SessionActivityFrame, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/sessions?window="+string(window), nil)
	if err != nil {
		return core.SessionActivityFrame{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return core.SessionActivityFrame{}, fmt.Errorf("session monitor: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return core.SessionActivityFrame{}, fmt.Errorf("session monitor HTTP %d", resp.StatusCode)
	}
	var frame core.SessionActivityFrame
	if err := json.NewDecoder(resp.Body).Decode(&frame); err != nil {
		return frame, err
	}
	return frame, nil
}
