//go:build !windows

package syncgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInterruptedGitRemovesItsIndexLock(t *testing.T) {
	hermetic(t)
	r := initRepo(t, filepath.Join(t.TempDir(), "library"), "")
	ready := filepath.Join(r.Root, "filter-ready")
	filter := filepath.Join(r.Root, ".git", "slow-filter")
	script := "#!/bin/sh\nprintf ready > " + shellQuote(ready) + "\nexec sleep 60\n"
	if err := os.WriteFile(filter, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	git(t, r.Root, "config", "filter.slow.clean", shellQuote(filter))
	if err := os.WriteFile(filepath.Join(r.Root, ".gitattributes"), []byte("slow.txt filter=slow\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Root, "slow.txt"), []byte("data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	var gitErr error
	go func() {
		defer close(done)
		gitErr = r.Locked(func() error {
			_, err := r.gitContext(ctx, "add", "--all")
			return err
		})
	}()
	defer func() { cancel(); <-done }()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("Git exited before entering the slow filter: %v", gitErr)
		case <-ctx.Done():
			t.Fatal("Git did not enter the slow filter")
		case <-time.After(10 * time.Millisecond):
		}
	}
	lock := filepath.Join(r.Root, ".git", "index.lock")
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("test never acquired an index lock: %v", err)
	}
	cancel()
	<-done
	if gitErr == nil || !strings.Contains(gitErr.Error(), "interrupted") {
		t.Fatalf("expected interruption: %v", gitErr)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("interrupted Git left its lock: %v", err)
	}
	// The transaction is usable immediately, without stale-lock recovery.
	git(t, r.Root, "config", "filter.slow.clean", "cat")
	syncRepo(t, r)
	clean(t, r)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
