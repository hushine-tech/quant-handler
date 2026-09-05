//go:build !windows

package codexcli

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

func configureCancellation(cmd *exec.Cmd, output io.Closer) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Close our reader first: descendants retaining stdout cannot block the
		// request, even if termination races process exit.
		_ = output.Close()
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
