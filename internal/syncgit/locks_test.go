package syncgit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/illegalstudio/prompt-library/internal/store"
)

func TestRecoverOrphanedIndexLock(t *testing.T) {
	hermetic(t)
	r := initRepo(t, filepath.Join(t.TempDir(), "library"), "")
	p, _ := store.New("Retained", "Unsynchronized local prompt", "")
	save(t, r, p)
	path := filepath.Join(r.Root, ".git", "index.lock")
	data := []byte("incomplete index retained for inspection")
	writeIndexLock(t, path, data, true)
	if err := r.Locked(func() error {
		if err := r.recoverIndexLock(func() (bool, error) { return false, nil }); err != nil {
			return err
		}
		return r.Sync()
	}); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(r.Root, ".git", "plib-recovered-index-*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one recovery file: %v %v", backups, err)
	}
	got, err := os.ReadFile(backups[0])
	if err != nil || string(got) != string(data) {
		t.Fatalf("lock contents lost: %q %v", got, err)
	}
	if got := find(t, r, p.ID); got.Body != p.Body {
		t.Fatal("prompt changed during recovery")
	}
	clean(t, r)
}

func TestIndexLockRecoveryRetainsUncertainLocks(t *testing.T) {
	for _, kind := range []string{"recent", "active-git", "process-scan-failed", "replaced", "modified", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			hermetic(t)
			r := initRepo(t, filepath.Join(t.TempDir(), "library"), "")
			path := filepath.Join(r.Root, ".git", "index.lock")
			writeIndexLock(t, path, []byte("keep"), kind != "recent")
			if kind == "symlink" {
				target := filepath.Join(r.Root, ".git", "other-lock")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			err := r.Locked(func() error {
				return r.recoverIndexLock(func() (bool, error) {
					switch kind {
					case "recent", "symlink":
						t.Fatal("unsafe lock should be rejected before the process scan")
					case "active-git":
						return true, nil
					case "process-scan-failed":
						return false, errors.New("process list unavailable")
					case "replaced":
						if err := os.Rename(path, path+".previous"); err != nil {
							t.Fatal(err)
						}
						writeIndexLock(t, path, []byte("keep"), true)
					case "modified":
						writeIndexLock(t, path, []byte("new index data"), true)
					}
					return false, nil
				})
			})
			if err == nil {
				t.Fatal("uncertain lock was recovered")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("lock was removed: %v", err)
			}
			backups, _ := filepath.Glob(filepath.Join(r.Root, ".git", "plib-recovered-index-*"))
			if len(backups) != 0 {
				t.Fatal("uncertain lock was archived")
			}
		})
	}
}

func writeIndexLock(t *testing.T, path string, data []byte, old bool) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if old {
		past := time.Now().Add(-2 * staleIndexLockAge)
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
	}
}

func TestContainsGitProcess(t *testing.T) {
	for _, tc := range []struct {
		name, output          string
		windows, git, invalid bool
	}{
		{name: "unix-git", output: "init\n/usr/bin/git\nps\n", git: true},
		{name: "unix-helper", output: "init\ngit-remote-http\n", git: true},
		{name: "unix-idle", output: "init\nplib\nps\n"},
		{name: "windows-git", output: "\"git.exe\",\"42\",\"Console\",\"1\",\"20 K\"\r\n", windows: true, git: true},
		{name: "windows-helper", output: "\"git-remote-https.exe\",\"42\",\"Console\",\"1\",\"20 K\"\r\n", windows: true, git: true},
		{name: "windows-idle", output: "\"plib.exe\",\"42\",\"Console\",\"1\",\"20 K\"\r\n", windows: true},
		{name: "windows-malformed", output: "\"git.exe\n", windows: true, invalid: true},
		{name: "windows-unexpected", output: "unexpected output\n", windows: true, invalid: true},
		{name: "empty", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := containsGitProcess(tc.output, tc.windows)
			if got != tc.git || (err != nil) != tc.invalid {
				t.Fatalf("got git=%v err=%v", got, err)
			}
		})
	}
}

func TestGitProcessScanFindsRunningGit(t *testing.T) {
	cmd := exec.Command("git", "hash-object", "--stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		running, err := gitProcessesRunning()
		if err != nil {
			t.Fatal(err)
		}
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("running Git process was not detected")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSyncRetainsRecentIndexLock(t *testing.T) {
	hermetic(t)
	r := initRepo(t, filepath.Join(t.TempDir(), "library"), "")
	path := filepath.Join(r.Root, ".git", "index.lock")
	writeIndexLock(t, path, nil, false)
	err := r.Locked(r.Sync)
	if err == nil || !strings.Contains(err.Error(), "retry shortly") {
		t.Fatalf("expected concise pending-sync error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("recent lock removed")
	}
}
