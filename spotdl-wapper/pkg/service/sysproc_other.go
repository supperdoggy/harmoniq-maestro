//go:build !linux

package service

import "os/exec"

func configureSysProcAttr(cmd *exec.Cmd) {
	// No Linux-specific process attributes on non-Linux platforms.
	_ = cmd
}
