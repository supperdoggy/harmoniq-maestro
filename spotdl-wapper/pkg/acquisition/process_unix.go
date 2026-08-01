//go:build linux || darwin

package acquisition

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// configureProcessGroup ensures cancellation terminates downloader children
// such as ffmpeg as well as the top-level spotDL/yt-dlp process.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
}
