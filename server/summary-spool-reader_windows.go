//go:build windows

package server

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func summarySpoolWatcherSupported() bool { return true }

// readSummarySpoolFile opens the path once and reads at most limit+1 bytes
// from that same handle. FILE_FLAG_OPEN_REPARSE_POINT makes the opened
// directory entry, rather than a reparse target, authoritative. The handle is
// also opened with delete sharing so an atomic publisher can rename over the
// path while it is being read.
func readSummarySpoolFile(path string, limit int) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_DEVICE|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		_ = windows.CloseHandle(h)
		return nil, os.ErrInvalid
	}
	fileType, err := windows.GetFileType(h)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if fileType != windows.FILE_TYPE_DISK {
		_ = windows.CloseHandle(h)
		return nil, os.ErrInvalid
	}

	f := os.NewFile(uintptr(h), "summary-spool")
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, os.ErrInvalid
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, ErrSummaryOversize
	}
	return data, nil
}
