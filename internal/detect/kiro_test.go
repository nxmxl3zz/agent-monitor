package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectKiro_NeitherBinNorData(t *testing.T) {
	t.Setenv("OPENUSAGE_DETECT_BIN_DIRS", t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("KIRO_DATA_DIR", filepath.Join(t.TempDir(), "missing-data"))
	t.Setenv("KIRO_SESSIONS_DIR", filepath.Join(t.TempDir(), "missing-sessions"))
	setHome(t, t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "missing-xdg"))

	var result Result
	detectKiro(&result)

	if len(result.Tools) != 0 {
		t.Errorf("Tools = %d, want 0", len(result.Tools))
	}
	if len(result.Accounts) != 0 {
		t.Errorf("Accounts = %d, want 0", len(result.Accounts))
	}
}

func TestDetectKiro_DBOnlyRegistersAccount(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "data.sqlite3")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}

	t.Setenv("KIRO_DATA_DIR", dataDir)
	t.Setenv("KIRO_SESSIONS_DIR", filepath.Join(t.TempDir(), "missing-sessions"))
	t.Setenv("OPENUSAGE_DETECT_BIN_DIRS", t.TempDir())
	t.Setenv("PATH", "")

	var result Result
	detectKiro(&result)

	if len(result.Accounts) != 1 {
		t.Fatalf("Accounts = %d, want 1", len(result.Accounts))
	}
	acct := result.Accounts[0]
	if acct.ID != "kiro-cli" {
		t.Errorf("acct.ID = %q, want kiro-cli", acct.ID)
	}
	if acct.Provider != "kiro_cli" {
		t.Errorf("acct.Provider = %q, want kiro_cli", acct.Provider)
	}
	if acct.Auth != "local" {
		t.Errorf("acct.Auth = %q, want local", acct.Auth)
	}
	if got := acct.Path("db_path", ""); got != dbPath {
		t.Errorf("db_path = %q, want %q", got, dbPath)
	}
}

func TestDetectKiro_BinaryRegistersTool(t *testing.T) {
	binDir := t.TempDir()
	binPath := writeFakeBinary(t, binDir, "kiro")

	t.Setenv("OPENUSAGE_DETECT_BIN_DIRS", binDir)
	t.Setenv("PATH", "")
	t.Setenv("KIRO_DATA_DIR", filepath.Join(t.TempDir(), "missing-data"))
	t.Setenv("KIRO_SESSIONS_DIR", filepath.Join(t.TempDir(), "missing-sessions"))

	var result Result
	detectKiro(&result)

	if len(result.Tools) != 1 {
		t.Fatalf("Tools = %d, want 1", len(result.Tools))
	}
	if result.Tools[0].Name != "Kiro CLI" {
		t.Errorf("Tool.Name = %q, want Kiro CLI", result.Tools[0].Name)
	}
	if len(result.Accounts) != 1 {
		t.Fatalf("Accounts = %d, want 1", len(result.Accounts))
	}
	if result.Accounts[0].Binary != binPath {
		t.Errorf("Account.Binary = %q, want %q", result.Accounts[0].Binary, binPath)
	}
}
