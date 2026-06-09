//go:build windows

package telemetry

import (
	"path/filepath"

	"github.com/janekbaraniewski/openusage/internal/config"
)

// platformStateDir keeps telemetry state beside settings.json under
// %APPDATA%\openusage\state on Windows, which has no ~/.local/state convention.
func platformStateDir() (string, error) {
	return filepath.Join(config.ConfigDir(), "state"), nil
}
