//go:build !linux && !darwin

package acquisition

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}
