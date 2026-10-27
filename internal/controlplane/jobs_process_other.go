//go:build !linux && !darwin

package controlplane

import (
	"os/exec"
	"time"
)

func configureCLIProcess(command *exec.Cmd) {
	command.WaitDelay = 3 * time.Second
}
