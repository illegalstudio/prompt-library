//go:build !windows

package syncgit

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func gitSignalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

func prepareGitCommand(cmd *exec.Cmd) {
	// Git handles SIGTERM by removing its lock files. Include Git's helpers.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

func finishInterruptedGit(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// WaitDelay bounds Git's shutdown; also stop any remaining helpers.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
