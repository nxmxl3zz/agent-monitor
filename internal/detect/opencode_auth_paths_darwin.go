//go:build darwin

package detect

import "path/filepath"

// opencodeAuthPlatformPaths returns the non-XDG_DATA_HOME candidate locations
// for OpenCode's auth.json on macOS. OpenCode defaults to the XDG path on darwin
// too; we additionally probe the Apple-native Application Support location for
// users who pinned it there.
func opencodeAuthPlatformPaths(home string) []string {
	return []string{
		filepath.Join(home, ".local", "share", "opencode", "auth.json"),
		filepath.Join(home, "Library", "Application Support", "opencode", "auth.json"),
	}
}
