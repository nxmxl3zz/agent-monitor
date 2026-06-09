//go:build !windows && !darwin

package detect

import "path/filepath"

// opencodeAuthPlatformPaths returns the non-XDG_DATA_HOME candidate location for
// OpenCode's auth.json on Linux and other Unix: ~/.local/share/opencode.
func opencodeAuthPlatformPaths(home string) []string {
	return []string{filepath.Join(home, ".local", "share", "opencode", "auth.json")}
}
