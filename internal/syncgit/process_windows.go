package syncgit

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
)

func gitSignalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt)
}

func prepareGitCommand(cmd *exec.Cmd) {}

func finishInterruptedGit(cmd *exec.Cmd) {}
