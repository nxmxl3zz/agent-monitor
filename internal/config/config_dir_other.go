//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

// osConfigDir returns the OpenUsage config directory on Unix: ~/.config/openusage.
// XDG_CONFIG_HOME is intentionally not honored (see docs/reference/paths.md).
func osConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "openusage")
}
