package syncgit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/illegalstudio/prompt-library/internal/store"
)

func hermetic(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}
func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "user.name=Test", "-c", "user.email=test@localhost", "-C", root}, args...)...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
func initRepo(t *testing.T, root, remote string) Repo {
	t.Helper()
	r := Repo{Root: root}
	if err := r.Locked(func() error {
		if err := r.Init(); err != nil {
			return err
		}
		if remote != "" {
			if err := r.SetRemote(remote); err != nil {
				return err
			}
		}
		return r.MarkConfigured()
	}); err != nil {
		t.Fatal(err)
	}
	return r
}
func syncRepo(t *testing.T, r Repo) {
	t.Helper()
	if err := r.Locked(r.Sync); err != nil {
		t.Fatal(err)
	}
}
func clean(t *testing.T, r Repo) {
	t.Helper()
	if status := git(t, r.Root, "status", "--porcelain"); status != "" {
		t.Fatalf("dirty repository: %s", status)
	}
	if _, err := os.Stat(filepath.Join(r.Root, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatal("merge still in progress")
	}
}
func save(t *testing.T, r Repo, p store.Prompt) {
	t.Helper()
	if err := (store.Store{Root: r.Root}).Save(p); err != nil {
		t.Fatal(err)
	}
}
func find(t *testing.T, r Repo, id string) store.Prompt {
	t.Helper()
	p, err := (store.Store{Root: r.Root}).Find(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func setupPair(t *testing.T) (Repo, Repo, string, store.Prompt) {
	t.Helper()
	hermetic(t)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	git(t, root, "init", "--bare", "--initial-branch=main", remote)
	a := initRepo(t, filepath.Join(root, "a"), remote)
	p, _ := store.New("Review", "Original\nmultiline prompt", "")
	save(t, a, p)
	syncRepo(t, a)
	b := initRepo(t, filepath.Join(root, "b"), remote)
	syncRepo(t, b)
	if find(t, b, p.ID).Body != p.Body {
		t.Fatal("second-machine bootstrap failed")
	}
	return a, b, remote, p
}
func TestOfflineLocalCommitAndRecovery(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	r := initRepo(t, filepath.Join(root, "library"), filepath.Join(root, "missing.git"))
	p, _ := store.New("Local", "Offline body", "")
	save(t, r, p)
	if err := r.Locked(r.Sync); err == nil {
		t.Fatal("expected unavailable remote warning")
	}
	clean(t, r)
	if find(t, r, p.ID).Body != p.Body {
		t.Fatal("offline prompt lost")
	}
	git(t, root, "init", "--bare", "--initial-branch=main", filepath.Join(root, "missing.git"))
	syncRepo(t, r)
	clean(t, r)
}
func TestConcurrentContentConflict(t *testing.T) {
	a, b, _, p := setupPair(t)
	local := p
	local.Body = "Version from A"
	local.Updated = p.Updated.Add(time.Second)
	save(t, a, local)
	syncRepo(t, a)
	remote := p
	remote.Body = "Version from B"
	remote.Updated = p.Updated.Add(2 * time.Second)
	save(t, b, remote)
	syncRepo(t, b)
	clean(t, b)
	if got := find(t, b, p.ID); got.Body != remote.Body {
		t.Fatalf("local version not kept: %+v", got)
	}
	path := (store.Store{Root: b.Root}).Path(p)
	conflicts, err := filepath.Glob(path + ".*.conflicted")
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("backup missing: %v %v", conflicts, err)
	}
	raw, err := os.ReadFile(conflicts[0])
	if err != nil {
		t.Fatal(err)
	}
	var alternative store.Prompt
	if json.Unmarshal(raw, &alternative) != nil || alternative.Body != local.Body {
		t.Fatal("alternative version lost")
	}
	syncRepo(t, a)
	clean(t, a)
	if find(t, a, p.ID).Body != remote.Body {
		t.Fatal("resolved version did not reach other machine")
	}
}
func TestMergeUsageAndIndependentFields(t *testing.T) {
	a, b, _, p := setupPair(t)
	x := p
	x.Title = "New title"
	x.LastUsed = p.Created.Add(5 * time.Hour)
	save(t, a, x)
	syncRepo(t, a)
	y := p
	y.Body = "New body"
	y.LastUsed = p.Created.Add(time.Hour)
	save(t, b, y)
	syncRepo(t, b)
	clean(t, b)
	got := find(t, b, p.ID)
	if got.Title != x.Title || got.Body != y.Body || !got.LastUsed.Equal(x.LastUsed) {
		t.Fatalf("semantic merge failed: %+v", got)
	}
	conflicts, _ := filepath.Glob((store.Store{Root: b.Root}).Path(p) + ".*.conflicted")
	if len(conflicts) != 0 {
		t.Fatal("compatible changes incorrectly archived")
	}
}
func TestModifyDeleteConflictKeepsContent(t *testing.T) {
	for _, localDelete := range []bool{false, true} {
		t.Run(map[bool]string{true: "local-delete", false: "remote-delete"}[localDelete], func(t *testing.T) {
			a, b, _, p := setupPair(t)
			if localDelete {
				p.Body = "Remote edit"
				save(t, a, p)
				syncRepo(t, a)
				if err := os.Remove((store.Store{Root: b.Root}).Path(p)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove((store.Store{Root: a.Root}).Path(p)); err != nil {
					t.Fatal(err)
				}
				syncRepo(t, a)
				p.Body = "Local edit"
				save(t, b, p)
			}
			syncRepo(t, b)
			clean(t, b)
			if got := find(t, b, p.ID); got.Body != p.Body {
				t.Fatal("modified prompt lost")
			}
		})
	}
}
func TestUnrelatedLibrariesAndMasterBranch(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	git(t, root, "init", "--bare", "--initial-branch=master", remote)
	a := initRepo(t, filepath.Join(root, "a"), "")
	git(t, a.Root, "symbolic-ref", "HEAD", "refs/heads/master")
	p, _ := store.New("A", "body A", "")
	save(t, a, p)
	syncRepo(t, a)
	if err := a.SetRemote(remote); err != nil {
		t.Fatal(err)
	}
	syncRepo(t, a)
	b := initRepo(t, filepath.Join(root, "b"), "")
	q, _ := store.New("B", "body B", "")
	save(t, b, q)
	syncRepo(t, b)
	if err := b.SetRemote(remote); err != nil {
		t.Fatal(err)
	}
	syncRepo(t, b)
	clean(t, b)
	ps, _, err := (store.Store{Root: b.Root}).List("", "", true)
	if err != nil || len(ps) != 2 {
		t.Fatal("unrelated history lost prompts")
	}
	syncRepo(t, a)
	if find(t, a, q.ID).Body != q.Body {
		t.Fatal("master branch sync failed")
	}
}
func TestResumeInterruptedMerge(t *testing.T) {
	a, b, _, p := setupPair(t)
	x := p
	x.Body = "A"
	save(t, a, x)
	syncRepo(t, a)
	y := p
	y.Body = "B"
	save(t, b, y)
	if err := b.commit(); err != nil {
		t.Fatal(err)
	}
	git(t, b.Root, "fetch", "origin")
	if _, err := b.git("merge", "--no-edit", "origin/main"); err == nil {
		t.Fatal("expected merge conflict")
	}
	syncRepo(t, b)
	clean(t, b)
	if find(t, b, p.ID).Body != "B" {
		t.Fatal("resume lost local version")
	}
}

func TestConcurrentPushRetriesPreserveBothPrompts(t *testing.T) {
	a, b, _, _ := setupPair(t)
	p, _ := store.New("From A", "Concurrent addition A", "")
	q, _ := store.New("From B", "Concurrent addition B", "")
	save(t, a, p)
	save(t, b, q)
	results := make(chan error, 2)
	go func() { results <- a.Locked(a.Sync) }()
	go func() { results <- b.Locked(b.Sync) }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	syncRepo(t, a)
	syncRepo(t, b)
	clean(t, a)
	clean(t, b)
	for _, r := range []Repo{a, b} {
		if find(t, r, p.ID).Body != p.Body || find(t, r, q.ID).Body != q.Body {
			t.Fatal("a concurrent push lost a prompt")
		}
	}
}

func TestConflictRestoresValidRecordAndArchivesDamagedLocal(t *testing.T) {
	a, b, _, p := setupPair(t)
	p.Body = "Healthy remote edit"
	save(t, a, p)
	syncRepo(t, a)
	path := (store.Store{Root: b.Root}).Path(p)
	broken := []byte("{\n  broken local record\n}\n")
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	syncRepo(t, b)
	clean(t, b)
	if find(t, b, p.ID).Body != p.Body {
		t.Fatal("did not restore the usable remote version")
	}
	matches, _ := filepath.Glob(path + ".*.conflicted")
	if len(matches) != 1 {
		t.Fatal("damaged version not archived")
	}
	data, err := os.ReadFile(matches[0])
	if err != nil || string(data) != string(broken) {
		t.Fatal("damaged version was lost")
	}
}
