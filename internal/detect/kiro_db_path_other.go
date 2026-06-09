//go:build !windows && !darwin

package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// kiroDBPlatformPath returns Kiro CLI's data.sqlite3 location on Linux and other
// Unix: $XDG_DATA_HOME/kiro-cli or ~/.local/share/kiro-cli.
func kiroDBPlatformPath(home string) string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "kiro-cli", "data.sqlite3")
	}
	return filepath.Join(home, ".local", "share", "kiro-cli", "data.sqlite3")
}
