//go:build windows

package hostexec

import (
	"errors"
	"os"
	"syscall"
)

// createNewProcessGroup starts the child in a new process group so a stop
// does not signal unrelated processes.
const createNewProcessGroup = 0x200

// sysProcAttr returns the child attributes for Windows: a new process
// group. A full Job Object teardown is a separate deployment concern and is
// deliberately not attempted here.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// signalGroupProc terminates the child. Windows has no graceful cross-process
// signal, so the graceful step is a direct terminate.
func signalGroupProc(p *os.Process) error {
	return p.Kill()
}

// killGroupProc terminates the child. It is idempotent: killing an
// already-exited process is not an error.
func killGroupProc(p *os.Process) error {
	if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// validateExecBit is a no-op on Windows: there is no executable mode bit.
func validateExecBit(info os.FileInfo) error {
	return nil
}
