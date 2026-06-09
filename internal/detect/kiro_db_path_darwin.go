//go:build darwin

package detect

import "path/filepath"

// kiroDBPlatformPath returns Kiro CLI's data.sqlite3 location on macOS.
func kiroDBPlatformPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "kiro-cli", "data.sqlite3")
}
