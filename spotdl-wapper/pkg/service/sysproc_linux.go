//go:build linux

package service

import (
	"os/exec"
	"syscall"
)

func configureSysProcAttr(cmd *exec.Cmd) {
	// Kill the child process when the parent dies on Linux.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
}
