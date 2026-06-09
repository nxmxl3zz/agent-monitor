//go:build !windows

package pricing

import (
	"os"
	"path/filepath"
)

// platformCustomOverridesPath uses ~/.config/openusage on Unix. An empty path
// (with nil error) is returned when the home dir can't be resolved, matching
// the historical best-effort behavior.
func platformCustomOverridesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", nil
	}
	return filepath.Join(home, ".config", "openusage", CustomOverridesFilename), nil
}
