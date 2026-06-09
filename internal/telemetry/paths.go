package telemetry

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultStateDir resolves the OpenUsage telemetry state directory (db, socket,
// spools, logs). XDG_STATE_HOME wins on every platform when set; otherwise the
// base is provided by platformStateDir() in the platform-specific
// state_dir_*.go files.
func DefaultStateDir() (string, error) {
	if base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); base != "" {
		return filepath.Join(base, "openusage"), nil
	}
	return platformStateDir()
}

func DefaultDBPath() (string, error) {
	stateDir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "telemetry.db"), nil
}

func DefaultSocketPath() (string, error) {
	stateDir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "telemetry.sock"), nil
}

func DefaultHookSpoolDir() (string, error) {
	stateDir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "hook-spool"), nil
}
