//go:build unix

package hostexec

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// sysProcAttr returns the child attributes that isolate the child in its
// own process group, so a stop tears down the whole tree.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// signalGroupProc sends the graceful terminate signal to the child's whole
// process group.
func signalGroupProc(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGTERM)
}

// killGroupProc hard-kills the child's whole process group. Killing an
// already-exited group is not an error.
func killGroupProc(p *os.Process) error {
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// validateExecBit refuses a file without any executable bit.
func validateExecBit(info os.FileInfo) error {
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("file is not executable")
	}
	return nil
}
