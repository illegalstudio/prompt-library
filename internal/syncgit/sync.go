package syncgit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/illegalstudio/prompt-library/internal/store"
)

type Repo struct{ Root string }

// Locked serializes repository transactions across processes, with OS-released locks.
func (r Repo) Locked(fn func() error) error {
	if err := os.MkdirAll(r.Root, 0700); err != nil {
		return err
	}
	lock := flock.New(filepath.Clean(r.Root) + ".lock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !ok {
		return fmt.Errorf("library is busy, retry shortly: %v", err)
	}
	defer lock.Unlock()
	return fn()
}

func (r Repo) git(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return r.gitContext(ctx, args...)
}

func (r Repo) gitContext(ctx context.Context, args ...string) ([]byte, error) {
	ctx, stop := gitSignalContext(ctx)
	defer stop()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "commit.gpgsign=false", "-c", "merge.renames=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "user.name=plib", "-c", "user.email=plib@localhost", "-C", r.Root}, args...)...)
	cmd.WaitDelay = time.Second
	prepareGitCommand(cmd)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GIT_DIR" || key == "GIT_WORK_TREE" || key == "GIT_INDEX_FILE" || key == "GIT_COMMON_DIR" || key == "GIT_OBJECT_DIRECTORY" || key == "GIT_ALTERNATE_OBJECT_DIRECTORIES" || key == "GIT_CONFIG_COUNT" || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") || key == "GIT_TERMINAL_PROMPT" || key == "GIT_SSH_COMMAND" || key == "GCM_INTERACTIVE" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=5 -o ServerAliveCountMax=1", "GCM_INTERACTIVE=never")
	b, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		finishInterruptedGit(cmd)
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return b, fmt.Errorf("git %s interrupted", args[0])
		}
		return b, fmt.Errorf("git %s timed out", args[0])
	}
	if err != nil {
		return b, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func (r Repo) Init() error {
	info, err := os.Lstat(filepath.Join(r.Root, ".git"))
	if errors.Is(err, os.ErrNotExist) {
		_, err = r.git("init", "--initial-branch=main")
	} else if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("library must have its own .git directory")
	}
	if err != nil {
		return err
	}
	// Recovery and temporary files are committed only after being finalized.
	exclude := filepath.Join(r.Root, ".git", "info", "exclude")
	b, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !strings.Contains(string(b), ".plib-write-*") {
		if err := store.AtomicWrite(r.Root, exclude, append(b, []byte("\n.plib-write-*\n")...)); err != nil {
			return err
		}
	}
	return nil
}

func (r Repo) Configured() bool {
	_, err := os.Stat(filepath.Join(r.Root, ".git", "plib-initialized"))
	return err == nil
}
func (r Repo) MarkConfigured() error {
	return store.AtomicWrite(r.Root, filepath.Join(r.Root, ".git", "plib-initialized"), []byte("1\n"))
}
func (r Repo) Remote() string {
	b, err := r.git("remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
func (r Repo) SetRemote(remote string) error {
	if strings.HasPrefix(remote, "-") || strings.TrimSpace(remote) == "" {
		return errors.New("remote must be a URL or filesystem path")
	}
	action := "add"
	if r.Remote() != "" {
		action = "set-url"
	}
	_, err := r.git("remote", action, "origin", remote)
	return err
}
func (r Repo) RemoveRemote() error {
	if r.Remote() == "" {
		return nil
	}
	_, err := r.git("remote", "remove", "origin")
	return err
}

func (r Repo) commit() error {
	if err := r.resolveScopeCopies(); err != nil {
		return err
	}
	if _, err := r.git("add", "--all"); err != nil {
		return err
	}
	b, err := r.git("status", "--porcelain")
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	_, err = r.git("commit", "-m", "chore: sync prompt library")
	return err
}

// Sync finishes local merges before reporting network failures. It never force-pushes.
// The caller must hold Locked, including the prompt mutation that precedes Sync.
func (r Repo) Sync() error {
	if err := r.recoverIndexLock(gitProcessesRunning); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(r.Root, ".git", "MERGE_HEAD")); err == nil {
		if err := r.resolve(); err != nil {
			return err
		}
		if _, err := r.git("commit", "--no-edit"); err != nil {
			return err
		}
	}
	if err := r.commit(); err != nil {
		return err
	}
	if r.Remote() == "" {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := r.git("fetch", "--prune", "origin"); err != nil {
			return err
		}
		branch, err := r.remoteBranch()
		if err != nil {
			return err
		}
		if branch != "" {
			if _, err := r.git("rev-parse", "--verify", "HEAD"); err != nil {
				if _, err := r.git("checkout", "-B", branch, "refs/remotes/origin/"+branch); err != nil {
					return err
				}
			} else {
				if _, err := r.git("merge", "--no-edit", "--allow-unrelated-histories", "refs/remotes/origin/"+branch); err != nil {
					if _, statErr := os.Stat(filepath.Join(r.Root, ".git", "MERGE_HEAD")); statErr != nil {
						return err
					}
					if err := r.resolve(); err != nil {
						return err
					}
					if _, err := r.git("commit", "--no-edit"); err != nil {
						return err
					}
				}
			}
		} else {
			b, err := r.git("symbolic-ref", "--short", "HEAD")
			if err != nil {
				return err
			}
			branch = strings.TrimSpace(string(b))
		}
		if _, err := r.git("rev-parse", "--verify", "HEAD"); err != nil {
			return nil
		}
		if err := r.commit(); err != nil {
			return err
		}
		if _, err := r.git("push", "--set-upstream", "origin", "HEAD:refs/heads/"+branch); err == nil {
			return nil
		} else if attempt == 2 {
			return err
		}
	}
	return nil
}

// Scope changes can put the same ID in two folders after concurrent moves or
// an interrupted local move. Keep the latest edit and archive every alternative.
func (r Repo) resolveScopeCopies() error {
	s := store.Store{Root: r.Root}
	prompts, _, err := s.List("", "", true)
	if err != nil {
		return err
	}
	sort.Slice(prompts, func(i, j int) bool {
		if !prompts[i].Updated.Equal(prompts[j].Updated) {
			return prompts[i].Updated.After(prompts[j].Updated)
		}
		return s.Path(prompts[i]) < s.Path(prompts[j])
	})
	seen := make(map[string]store.Prompt)
	for _, p := range prompts {
		winner, duplicate := seen[p.ID]
		if !duplicate {
			seen[p.ID] = p
			continue
		}
		path := s.Path(p)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		backup := fmt.Sprintf("%s.%d.conflicted", path, time.Now().UnixNano())
		if err := store.AtomicWrite(r.Root, backup, data); err != nil {
			return err
		}
		if p.LastUsed.After(winner.LastUsed) {
			winner.LastUsed = p.LastUsed
			if err := s.Save(winner); err != nil {
				return err
			}
			seen[p.ID] = winner
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (r Repo) remoteBranch() (string, error) {
	// Ask for the advertised default, including on a second machine with a different initial branch.
	b, err := r.git("ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "ref: refs/heads/") {
			ref, _, _ := strings.Cut(strings.TrimPrefix(line, "ref: refs/heads/"), "\t")
			return ref, nil
		}
	}
	b, err = r.git("for-each-ref", "--format=%(refname:strip=3)", "refs/remotes/origin")
	if err != nil {
		return "", err
	}
	branches := strings.Fields(string(b))
	for _, preferred := range []string{"main", "master"} {
		for _, branch := range branches {
			if branch == preferred {
				return branch, nil
			}
		}
	}
	if len(branches) > 0 {
		sort.Strings(branches)
		return branches[0], nil
	}
	return "", nil
}

func (r Repo) resolve() error {
	b, err := r.git("diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(string(b), "\x00") {
		if name == "" {
			continue
		}
		if filepath.IsAbs(name) || strings.HasPrefix(filepath.Clean(name), "..") {
			return errors.New("unsafe conflict path")
		}
		// Read blobs from the index, never copy conflict markers into active prompts.
		base, _ := r.git("show", ":1:"+name)
		ours, oursErr := r.git("show", ":2:"+name)
		theirs, theirsErr := r.git("show", ":3:"+name)
		if oursErr != nil {
			ours = nil
		}
		if theirsErr != nil {
			theirs = nil
		}
		chosen := ours
		alternative := theirs
		archive := false
		if oursErr != nil {
			chosen = theirs
			alternative = theirs
			archive = true
		} else if theirsErr != nil {
			alternative = ours
			archive = true
		} else if strings.HasSuffix(name, ".json") {
			chosen, alternative, archive = mergePrompt(base, ours, theirs)
		} else {
			archive = string(ours) != string(theirs)
		}
		if archive {
			suffix := fmt.Sprintf(".%d.conflicted", time.Now().UnixNano())
			if err := store.AtomicWrite(r.Root, filepath.Join(r.Root, filepath.FromSlash(name))+suffix, alternative); err != nil {
				return err
			}
		}
		path := filepath.Join(r.Root, filepath.FromSlash(name))
		if oursErr != nil && theirsErr != nil {
			if _, err := r.git("rm", "-f", "--", name); err != nil {
				return err
			}
		} else {
			// Replacing the entry also neutralizes a conflicted symlink.
			if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
			if err := store.AtomicWrite(r.Root, path, chosen); err != nil {
				return err
			}
		}
	}
	if _, err := r.git("add", "--all"); err != nil {
		return err
	}
	return nil
}

func mergePrompt(base, ours, theirs []byte) ([]byte, []byte, bool) {
	var b, o, t store.Prompt
	validOurs := json.Unmarshal(ours, &o) == nil && store.Validate(o) == nil
	validTheirs := json.Unmarshal(theirs, &t) == nil && store.Validate(t) == nil
	if !validOurs && validTheirs {
		return theirs, ours, true
	}
	if !validOurs || !validTheirs || o.ID != t.ID {
		return ours, theirs, true
	}
	hasBase := json.Unmarshal(base, &b) == nil
	conflict := false
	merge := func(old, local, remote string) string {
		if local == remote {
			return local
		}
		if hasBase && local == old {
			return remote
		}
		if hasBase && remote == old {
			return local
		}
		conflict = true
		return local
	}
	o.Title = merge(b.Title, o.Title, t.Title)
	o.Body = merge(b.Body, o.Body, t.Body)
	o.Project = merge(b.Project, o.Project, t.Project)
	if t.Updated.After(o.Updated) {
		o.Updated = t.Updated
	}
	if t.LastUsed.After(o.LastUsed) {
		o.LastUsed = t.LastUsed
	}
	if t.Created.Before(o.Created) {
		o.Created = t.Created
	}
	out, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return ours, theirs, true
	}
	return append(out, '\n'), theirs, conflict
}
