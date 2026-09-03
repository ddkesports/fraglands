//go:build !windows

package server

import (
	"io"
	"os"
	"syscall"
)

func summarySpoolWatcherSupported() bool { return true }

// readSummarySpoolFile opens one path once, refuses symlinks and non-regular
// files, and reads at most limit+1 bytes from that same handle. Atomic rename
// publication therefore cannot expose an incomplete writer handle.
func readSummarySpoolFile(path string, limit int) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "summary-spool")
	if f == nil {
		_ = syscall.Close(fd)
		return nil, os.ErrInvalid
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, ErrSummaryOversize
	}
	return data, nil
}
