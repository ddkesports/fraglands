//go:build windows

package server

import "errors"

func summarySpoolWatcherSupported() bool { return false }

func readSummarySpoolFile(string, int) ([]byte, error) {
	return nil, errors.New("server: no-follow regular-file spool read unavailable on windows")
}
