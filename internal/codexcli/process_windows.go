//go:build windows

package codexcli

import (
	"io"
	"os/exec"
	"strconv"
)

func configureCancellation(cmd *exec.Cmd, output io.Closer) {
	cmd.Cancel = func() error {
		_ = output.Close()
		// taskkill terminates the launcher and its descendants on Windows.
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		return cmd.Process.Kill()
	}
}

// Windows does not support File.Sync on directory handles. The state file is
// synced before the atomic replacement; the supported deployment is macOS/Linux.
func syncDirectory(string) error { return nil }
