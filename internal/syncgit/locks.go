package syncgit

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const staleIndexLockAge = time.Minute

// The caller holds Repo.Locked. Never infer ownership from the lock's age alone.
func (r Repo) recoverIndexLock(gitRunning func() (bool, error)) error {
	path := filepath.Join(r.Root, ".git", "index.lock")
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("Git index lock is not a regular file; retained at %s", path)
	}
	if time.Since(before.ModTime()) < staleIndexLockAge {
		return fmt.Errorf("Git index lock is recent; retry shortly (retained at %s)", path)
	}
	running, err := gitRunning()
	if err != nil {
		return fmt.Errorf("cannot verify active Git processes; index lock retained at %s: %w", path, err)
	}
	if running {
		return fmt.Errorf("a Git process is still active; retry shortly (index lock retained at %s)", path)
	}
	after, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		return fmt.Errorf("Git index lock changed during recovery; retry shortly (retained at %s)", path)
	}
	// Keep even an incomplete index for inspection without blocking the next sync.
	backup := filepath.Join(r.Root, ".git", fmt.Sprintf("plib-recovered-index-%d", time.Now().UnixNano()))
	if err := os.Rename(path, backup); err != nil {
		return fmt.Errorf("archive orphaned Git index lock: %w", err)
	}
	return nil
}

func gitProcessesRunning() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	name, args := "ps", []string{"-A", "-o", "comm="}
	if runtime.GOOS == "windows" {
		name, args = "tasklist", []string{"/FO", "CSV", "/NH"}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	b, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("list processes: %w", err)
	}
	return containsGitProcess(string(b), runtime.GOOS == "windows")
}

func containsGitProcess(output string, windows bool) (bool, error) {
	names := strings.Split(output, "\n")
	if windows {
		rows, err := csv.NewReader(strings.NewReader(output)).ReadAll()
		if err != nil {
			return false, fmt.Errorf("parse process list: %w", err)
		}
		names = nil
		for _, row := range rows {
			if len(row) != 5 {
				return false, errors.New("unexpected process list format")
			}
			names = append(names, row[0])
		}
	}
	if strings.TrimSpace(output) == "" {
		return false, errors.New("process list is empty")
	}
	for _, name := range names {
		name = strings.ToLower(filepath.Base(strings.TrimSpace(name)))
		name = strings.TrimSuffix(name, ".exe")
		if name == "git" || strings.HasPrefix(name, "git-") {
			return true, nil
		}
	}
	return false, nil
}
