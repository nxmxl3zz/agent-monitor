//go:build windows

package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// opencodeAuthPlatformPaths returns the non-XDG_DATA_HOME candidate locations
// for OpenCode's auth.json on Windows. OpenCode's `xdg-basedir` resolution has
// no Windows branch, so it writes to %USERPROFILE%\.local\share\opencode today;
// %LOCALAPPDATA% and %APPDATA% are forward-compat fallbacks.
func opencodeAuthPlatformPaths(home string) []string {
	paths := []string{filepath.Join(home, ".local", "share", "opencode", "auth.json")}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		paths = append(paths, filepath.Join(localAppData, "opencode", "auth.json"))
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		paths = append(paths, filepath.Join(appData, "opencode", "auth.json"))
	} else {
		paths = append(paths, filepath.Join(home, "AppData", "Roaming", "opencode", "auth.json"))
	}
	return paths
}
