//go:build windows

package pricing

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Windows file-sharing errors that clear within milliseconds: a concurrent
// reader holding the target open, or a reader opening a file mid-rename. Unlike
// Unix, Windows cannot rename onto (or, by default, read) a file another handle
// has open without the right share mode, so these ops can transiently fail.
const (
	errAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	errSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
)

func transientWindowsErr(err error) bool {
	return errors.Is(err, errAccessDenied) || errors.Is(err, errSharingViolation)
}

// retryTransient runs op up to ~20 times with a short linear backoff, retrying
// only on transient Windows sharing/lock errors. The total worst-case wait is
// ~210ms, far longer than the sub-millisecond window a concurrent reader keeps
// a small cache file open.
func retryTransient(op func() error) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = op(); err == nil || !transientWindowsErr(err) {
			return err
		}
		time.Sleep(time.Duration(i+1) * time.Millisecond)
	}
	return err
}

// atomicReplace renames tmp onto final, retrying when a concurrent reader
// momentarily holds final open.
func atomicReplace(tmp, final string) error {
	return retryTransient(func() error { return os.Rename(tmp, final) })
}

// readCacheFile reads path, retrying when a concurrent atomic replace momentarily
// blocks the open.
func readCacheFile(path string) ([]byte, error) {
	var data []byte
	err := retryTransient(func() error {
		var e error
		data, e = os.ReadFile(path)
		return e
	})
	return data, err
}
