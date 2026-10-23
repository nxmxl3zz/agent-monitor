package claude_code

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExpiredClaudeSessionDelegatesRotationOnce(t *testing.T) {
	expired := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"old","expiresAt":%d}}`, time.Now().Add(-time.Hour).UnixMilli())
	writeCredentials(t, expired)
	home, _ := os.UserHomeDir()
	calls := 0
	p := &Provider{oauthRefresh: func(context.Context) error {
		calls++
		updated := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"new","expiresAt":%d}}`, time.Now().Add(time.Hour).UnixMilli())
		return os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(updated), 0o600)
	}}
	for i := 0; i < 2; i++ {
		if err := p.refreshExpiredOAuth(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("rotations=%d, want 1", calls)
	}
	token, err := readClaudeCodeOAuthToken()
	if err != nil || token != "new" {
		t.Fatal("renewed credential was not re-read")
	}
}

func TestClaudeRenewalInvokesOnlyAuthenticationCommand(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell fixture requires Unix")
	}
	dir := t.TempDir()
	trace := filepath.Join(dir, "args.txt")
	t.Setenv("CLAUDE_REFRESH_TRACE", trace)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CLAUDE_REFRESH_TRACE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := refreshClaudeOAuthViaCLI(context.Background()); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(args)) != "auth\nstatus\n--json" {
		t.Fatalf("unexpected CLI invocation: %q", args)
	}
}

func TestFailedClaudeSessionRenewalIsThrottled(t *testing.T) {
	writeCredentials(t, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"old","expiresAt":%d}}`, time.Now().Add(-time.Hour).UnixMilli()))
	calls := 0
	p := &Provider{oauthRefresh: func(context.Context) error { calls++; return errors.New("offline") }}
	for i := 0; i < 2; i++ {
		if err := p.refreshExpiredOAuth(context.Background()); err == nil {
			t.Fatal("failed refresh reported success")
		}
	}
	if calls != 1 {
		t.Fatal("failed refresh retried without cooldown")
	}
}
