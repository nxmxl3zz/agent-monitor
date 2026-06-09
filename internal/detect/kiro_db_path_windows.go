//go:build windows

package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// kiroDBPlatformPath returns Kiro CLI's data.sqlite3 location on Windows. Kiro
// (formerly Amazon Q Developer CLI) is Rust-based and stores data via the `dirs`
// crate's data dir, which is %APPDATA% on Windows (the Roaming AppData root) —
// the analogue of the ~/.local/share path used on Linux.
func kiroDBPlatformPath(home string) string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "kiro-cli", "data.sqlite3")
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		return filepath.Join(appData, "kiro-cli", "data.sqlite3")
	}
	return filepath.Join(home, "AppData", "Roaming", "kiro-cli", "data.sqlite3")
}
