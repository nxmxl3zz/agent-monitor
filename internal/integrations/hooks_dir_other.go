//go:build !windows

package integrations

import "path/filepath"

// platformHooksDir places OpenUsage's hook scripts under <configRoot>/openusage/hooks
// on Unix (configRoot is XDG_CONFIG_HOME or ~/.config).
func platformHooksDir(configRoot string) string {
	return filepath.Join(configRoot, "openusage", "hooks")
}
