//go:build !windows

package pricing

import "os"

// atomicReplace renames tmp onto final. On Unix this is atomic even while a
// reader holds final open, so no retry is needed.
func atomicReplace(tmp, final string) error {
	return os.Rename(tmp, final)
}

// readCacheFile reads path. On Unix a concurrent rename onto path never blocks
// the read, so no retry is needed.
func readCacheFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
