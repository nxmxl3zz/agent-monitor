//go:build !windows

package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
)

// platformStateDir uses the XDG state convention ~/.local/state/openusage on Unix.
func platformStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("telemetry: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "openusage"), nil
}
