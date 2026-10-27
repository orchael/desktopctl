//go:build linux || darwin

package controlplane

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureCLIProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 3 * time.Second
}
