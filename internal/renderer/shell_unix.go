//go:build !windows

package renderer

import (
	"os/exec"
	"syscall"
)

func shellCommand(command string) (string, []string) {
	return "/bin/sh", []string{"-c", command}
}

// isolate gives the shell its own process group and makes cancellation kill
// that group: /bin/sh on Linux forks the command instead of exec'ing it, so
// killing the shell alone would leave the renderer running with the pipe.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
