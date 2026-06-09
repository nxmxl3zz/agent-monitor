//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// osConfigDir returns the OpenUsage config directory on Windows: %APPDATA%\openusage.
func osConfigDir() string {
	return filepath.Join(os.Getenv("APPDATA"), "openusage")
}
