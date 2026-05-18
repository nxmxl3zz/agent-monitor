package detect

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/janekbaraniewski/openusage/internal/core"
)

// detectKiro registers a local Kiro CLI account when either its binary,
// file-session directory, or data.sqlite3 database is present.
func detectKiro(result *Result) {
	bin := findBinary("kiro")
	if bin == "" {
		// Kiro CLI is the renamed Amazon Q Developer CLI; older installs may
		// still expose the q binary while writing the same data.sqlite3 shape.
		bin = findBinary("q")
	}

	sessionsDir := defaultKiroSessionsDir()
	hasSessions := sessionsDir != "" && dirExists(sessionsDir)
	dbPath := defaultKiroDBPath()
	hasDB := dbPath != "" && fileExists(dbPath)

	if bin == "" && !hasSessions && !hasDB {
		return
	}

	configDir := defaultKiroConfigDir()
	if bin != "" {
		log.Printf("[detect] Found Kiro CLI at %s", bin)
		result.Tools = append(result.Tools, DetectedTool{
			Name:       "Kiro CLI",
			BinaryPath: bin,
			ConfigDir:  configDir,
			Type:       "cli",
		})
	}

	acct := core.AccountConfig{
		ID:           "kiro-cli",
		Provider:     "kiro_cli",
		Auth:         "local",
		Binary:       bin,
		RuntimeHints: make(map[string]string),
	}
	if hasSessions {
		acct.SetPath("sessions_dir", sessionsDir)
		acct.SetHint("sessions_dir", sessionsDir)
		log.Printf("[detect] Kiro CLI sessions dir at %s", sessionsDir)
	}
	if hasDB {
		acct.SetPath("db_path", dbPath)
		acct.SetHint("db_path", dbPath)
		log.Printf("[detect] Kiro CLI data.sqlite3 at %s", dbPath)
	}
	if configDir != "" {
		acct.SetHint("data_dir", configDir)
	}

	addAccount(result, acct)
}

func defaultKiroSessionsDir() string {
	if dir := strings.TrimSpace(os.Getenv("KIRO_SESSIONS_DIR")); dir != "" {
		return dir
	}
	home := homeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".kiro", "sessions", "cli")
}

func defaultKiroDBPath() string {
	if root := strings.TrimSpace(os.Getenv("KIRO_DATA_DIR")); root != "" {
		return filepath.Join(root, "data.sqlite3")
	}
	home := homeDir()
	if home == "" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "kiro-cli", "data.sqlite3")
	case "linux":
		if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
			return filepath.Join(xdg, "kiro-cli", "data.sqlite3")
		}
		return filepath.Join(home, ".local", "share", "kiro-cli", "data.sqlite3")
	default:
		if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
			return filepath.Join(xdg, "kiro-cli", "data.sqlite3")
		}
		return filepath.Join(home, ".local", "share", "kiro-cli", "data.sqlite3")
	}
}

func defaultKiroConfigDir() string {
	if dir := defaultKiroSessionsDir(); dir != "" {
		return filepath.Dir(filepath.Dir(dir))
	}
	if dbPath := defaultKiroDBPath(); dbPath != "" {
		return filepath.Dir(dbPath)
	}
	return ""
}
