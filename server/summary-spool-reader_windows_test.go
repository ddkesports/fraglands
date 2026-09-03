//go:build windows

package server

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReadSummarySpoolFileRegularAndOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runback_summary_gen3.json")
	if err := os.WriteFile(path, []byte("summary"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readSummarySpoolFile(path, len("summary"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "summary" {
		t.Fatalf("got %q, want summary", got)
	}
	if err := os.WriteFile(path, make([]byte, MaxTerminalSummaryBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSummarySpoolFile(path, MaxTerminalSummaryBytes); !errors.Is(err, ErrSummaryOversize) {
		t.Fatalf("got %v, want ErrSummaryOversize", err)
	}
}

func TestReadSummarySpoolFileRefusesReparsePoints(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Logf("symlinks unavailable: %v", err)
	} else if _, err := readSummarySpoolFile(link, 64); err == nil {
		t.Fatal("symlink was accepted")
	}

	targetDir := filepath.Join(dir, "target-dir")
	junction := filepath.Join(dir, "junction")
	if err := os.Mkdir(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, targetDir).Run(); err != nil {
		t.Skipf("junctions unavailable: %v", err)
	}
	if _, err := readSummarySpoolFile(junction, 64); err == nil {
		t.Fatal("junction was accepted")
	}
}

func TestReadSummarySpoolFileRefusesDeviceWithoutBlocking(t *testing.T) {
	start := time.Now()
	_, err := readSummarySpoolFile(`\\.\NUL`, 64)
	if err == nil {
		t.Fatal("device was accepted")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("device refusal took %s", elapsed)
	}
}

func TestReadSummarySpoolFileRefusesNamedPipeWithoutBlocking(t *testing.T) {
	pipePath := `\\.\pipe\fraglands-summary-reader-` + strconv.FormatInt(time.Now().UnixNano(), 10)
	name, err := windows.UTF16PtrFromString(pipePath)
	if err != nil {
		t.Fatal(err)
	}
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Skipf("named pipes unavailable: %v", err)
	}
	defer windows.CloseHandle(server)

	done := make(chan error, 1)
	go func() {
		_, err := readSummarySpoolFile(pipePath, 64)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("named pipe was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("named pipe refusal blocked")
	}
}

func TestReadSummarySpoolFileAllowsAtomicRenameWhileReading(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runback_summary_gen3.json")
	tmp := filepath.Join(dir, "publish.tmp")
	bak := filepath.Join(dir, "publish.retired")
	payload := make([]byte, MaxTerminalSummaryBytes)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}

	const iterations = 2000
	renameErrors := make(chan error, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for i := 0; i < iterations; i++ {
			if _, err := readSummarySpoolFile(path, MaxTerminalSummaryBytes); err != nil {
				// A publisher moving the current artifact aside between the
				// two rename steps makes the canonical path transiently
				// absent; the watcher treats that as "observe the next poll".
				if os.IsNotExist(err) {
					continue
				}
				t.Errorf("read iteration %d: %v", i, err)
				return
			}
		}
	}()
	for i := 0; i < iterations; i++ {
		if err := os.WriteFile(tmp, payload, 0600); err != nil {
			t.Fatal(err)
		}
		// Windows cannot rename a new file over a path whose existing file
		// has an open handle, even when that handle was opened with
		// FILE_SHARE_DELETE: MoveFileEx(REPLACE_EXISTING) fails with
		// ERROR_ACCESS_DENIED. Atomic publishers therefore move the current
		// artifact aside first, then move the replacement into place. Both
		// steps must succeed while the reader holds the file open; that is
		// only possible because the reader opens with FILE_SHARE_DELETE.
		if err := os.Rename(path, bak); err != nil {
			select {
			case renameErrors <- err:
			default:
			}
		}
		if err := os.Rename(tmp, path); err != nil {
			select {
			case renameErrors <- err:
			default:
			}
		}
	}
	<-readerDone
	select {
	case err := <-renameErrors:
		t.Fatalf("atomic rename while reading failed: %v", err)
	default:
	}
}
