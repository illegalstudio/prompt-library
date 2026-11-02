package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/illegalstudio/prompt-library/internal/store"
	"github.com/illegalstudio/prompt-library/internal/syncgit"
)

func deleteModel(t *testing.T, scope string) (model, store.Prompt) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home := filepath.Join(t.TempDir(), "library")
	a := app{store: store.Store{Root: home}, repo: syncgit.Repo{Root: home}, project: "github.com/acme/api"}
	p, err := store.New("Selected prompt", "Prompt body", scope)
	if err != nil {
		t.Fatal(err)
	}
	err = a.repo.Locked(func() error {
		if err := a.repo.Init(); err != nil {
			return err
		}
		if err := a.repo.MarkConfigured(); err != nil {
			return err
		}
		if err := a.store.Save(p); err != nil {
			return err
		}
		return a.repo.Sync()
	})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(a)
	m.busy = false
	return m, p
}

func TestDeleteConfirmationDefaultsToNo(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("n")},
		{Type: tea.KeyRunes, Runes: []rune("N")}, {Type: tea.KeyEsc},
	} {
		t.Run(key.String(), func(t *testing.T) {
			m, p := deleteModel(t, "")
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			m = next.(model)
			if cmd != nil || m.mode != "delete" || m.deleting == nil {
				t.Fatal("d must open a confirmation without deleting")
			}
			if !strings.Contains(m.View(), `Delete "Selected prompt"? [y/N]`) {
				t.Fatal("confirmation must identify the prompt and default to No")
			}
			// Confirmation must not keep the repository locked while waiting for input.
			if err := m.a.repo.Locked(func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			next, cmd = m.Update(key)
			m = next.(model)
			if cmd != nil || m.mode != "browse" || m.deleting != nil {
				t.Fatal("cancellation did not return to the list")
			}
			if _, err := m.a.store.Find(p.ID); err != nil {
				t.Fatal("cancellation removed the prompt:", err)
			}
		})
	}
}

func TestConfirmedDeleteRemovesOnlySelectedPrompt(t *testing.T) {
	for _, tt := range []struct{ name, scope, key string }{
		{"global", "", "y"}, {"project", "github.com/acme/api", "Y"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, p := deleteModel(t, tt.scope)
			other, _ := store.New("Keep this prompt", "Other body", "")
			if err := m.a.repo.Locked(func() error { return m.a.store.Save(other) }); err != nil {
				t.Fatal(err)
			}
			m.prompts = []store.Prompt{other, p}
			m.cursor = 1
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			m = next.(model)
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)})
			m = next.(model)
			if cmd == nil || !m.busy {
				t.Fatal("yes did not start deletion")
			}
			result := cmd().(resultMsg)
			if result.err != nil {
				t.Fatal(result.err)
			}
			next, _ = m.Update(result)
			m = next.(model)
			if m.mode != "browse" || m.cursor != 0 || len(m.prompts) != 1 || m.prompts[0].ID != other.ID {
				t.Fatal("list or selection not refreshed after deletion")
			}
			if _, err := os.Stat(m.a.store.Path(p)); !os.IsNotExist(err) {
				t.Fatal("selected prompt file remains")
			}
			if _, err := m.a.store.Find(other.ID); err != nil {
				t.Fatal("another prompt was removed")
			}
			output, err := exec.Command("git", "-C", m.a.repo.Root, "status", "--porcelain").CombinedOutput()
			if err != nil || len(output) != 0 {
				t.Fatalf("deletion was not committed: %s %v", output, err)
			}
		})
	}
}

func TestDeleteLastPromptWorksOffline(t *testing.T) {
	m, p := deleteModel(t, "")
	if err := m.a.repo.Locked(func() error { return m.a.repo.SetRemote(filepath.Join(t.TempDir(), "unavailable.git")) }); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = next.(model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(model)
	result := cmd().(resultMsg)
	if result.err != nil || !strings.Contains(result.status, "sync pending") {
		t.Fatalf("offline deletion failed: %+v", result)
	}
	next, _ = m.Update(result)
	m = next.(model)
	if len(m.prompts) != 0 || m.cursor != 0 || !strings.Contains(m.View(), "No prompts yet") {
		t.Fatal("last prompt deletion did not show the empty list")
	}
	if _, err := os.Stat(m.a.store.Path(p)); !os.IsNotExist(err) {
		t.Fatal("offline deletion left the file in place")
	}
}

func TestDeleteRejectsChangesAfterConfirmationOpened(t *testing.T) {
	m, p := deleteModel(t, "")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = next.(model)
	changed := p
	changed.Body = "Changed by another process"
	changed.Updated = p.Updated.Add(time.Second)
	if err := m.a.repo.Locked(func() error { return m.a.store.Save(changed) }); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	result := cmd().(resultMsg)
	if result.err == nil || !strings.Contains(result.err.Error(), "changed") {
		t.Fatal("a changed prompt was deleted without renewed confirmation")
	}
	saved, err := m.a.store.Find(p.ID)
	if err != nil || saved.Body != changed.Body {
		t.Fatal("concurrent edit was lost")
	}
}

func TestDeleteIgnoredOutsideIdleList(t *testing.T) {
	m, _ := deleteModel(t, "")
	for _, state := range []string{"empty", "busy", "search"} {
		t.Run(state, func(t *testing.T) {
			current := m
			switch state {
			case "empty":
				current.prompts = nil
			case "busy":
				current.busy = true
			case "search":
				current.mode = "search"
				current.search.Focus()
			}
			next, _ := current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			current = next.(model)
			if current.deleting != nil || current.mode == "delete" {
				t.Fatal("d opened deletion outside an idle nonempty list")
			}
			if state == "search" && current.search.Value() != "d" {
				t.Fatal("d was not entered into the search query")
			}
		})
	}
}

func TestDeletePreservesUnreviewedRemoteChanges(t *testing.T) {
	m, p := deleteModel(t, "")
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	if output, err := exec.Command("git", "init", "--bare", "--initial-branch=main", remote).CombinedOutput(); err != nil {
		t.Fatalf("init remote: %s %v", output, err)
	}
	if err := m.a.repo.Locked(func() error {
		if err := m.a.repo.SetRemote(remote); err != nil {
			return err
		}
		return m.a.repo.Sync()
	}); err != nil {
		t.Fatal(err)
	}
	peer := syncgit.Repo{Root: filepath.Join(base, "peer")}
	changed := p
	changed.Body = "Remote edit"
	changed.Updated = p.Updated.Add(time.Second)
	if err := peer.Locked(func() error {
		if err := peer.Init(); err != nil {
			return err
		}
		if err := peer.SetRemote(remote); err != nil {
			return err
		}
		if err := peer.Sync(); err != nil {
			return err
		}
		if err := (store.Store{Root: peer.Root}).Save(changed); err != nil {
			return err
		}
		return peer.Sync()
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = next.(model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(model)
	result := cmd().(resultMsg)
	if result.err != nil || !strings.Contains(result.status, "was kept") {
		t.Fatalf("remote recovery was not reported: %+v", result)
	}
	next, _ = m.Update(result)
	m = next.(model)
	if len(m.prompts) != 1 || m.prompts[0].Body != changed.Body {
		t.Fatal("unreviewed remote content was lost")
	}
	// After reviewing the updated record, another explicit confirmation can delete it.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = next.(model)
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	result = cmd().(resultMsg)
	if result.err != nil || len(result.prompts) != 0 || !strings.HasPrefix(result.status, "Deleted:") {
		t.Fatalf("reviewed prompt could not be deleted: %+v", result)
	}
}
