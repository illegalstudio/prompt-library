package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/illegalstudio/prompt-library/internal/store"
	"github.com/illegalstudio/prompt-library/internal/syncgit"
)

func run(t *testing.T, home, input string, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewCommand()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(append([]string{"--home", home}, args...))
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}
func TestCLILocalWorkflow(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home := filepath.Join(t.TempDir(), "library")
	if _, _, err := run(t, home, "", "init", "--local"); err != nil {
		t.Fatal(err)
	}
	body := "first line\nsecond line\n"
	out, _, err := run(t, home, body, "add", "--title", "Review", "--stdin")
	if err != nil || !strings.Contains(out, "Saved") {
		t.Fatal(out, err)
	}
	out, _, err = run(t, home, "", "list", "SECOND", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var ps []store.Prompt
	if json.Unmarshal([]byte(out), &ps) != nil || len(ps) != 1 {
		t.Fatal(out)
	}
	out, _, err = run(t, home, "", "show", ps[0].ID[:8])
	if err != nil || out != body {
		t.Fatalf("body round trip: %q %v", out, err)
	}
	if _, _, err := run(t, home, "", "add", "--title", "Invalid", "--body", " "); err == nil {
		t.Fatal("invalid prompt accepted")
	}
	if _, _, err := run(t, home, "", "init", "--local"); err == nil {
		t.Fatal("reinitialization accepted")
	}
}

func TestShowRemainsAvailableWithRecentGitLock(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home := filepath.Join(t.TempDir(), "library")
	if _, _, err := run(t, home, "", "init", "--local"); err != nil {
		t.Fatal(err)
	}
	p, _ := store.New("Local", "Exact\nmultiline body\n", "")
	if err := (store.Store{Root: home}).Save(p); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(home, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0600); err != nil {
		t.Fatal(err)
	}
	out, warning, err := run(t, home, "", "show", p.ID)
	if err != nil || out != p.Body || !strings.Contains(warning, "Sync pending; local data retained:") {
		t.Fatalf("local read failed: out=%q warning=%q err=%v", out, warning, err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("recent lock was removed")
	}
}
func TestTypingAndScopeToggle(t *testing.T) {
	m := newModel(app{store: store.Store{Root: t.TempDir()}, repo: syncgit.Repo{Root: t.TempDir()}, project: "github.com/acme/api"})
	m.beginAdd()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Title")})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(model)
	if !m.projectScope || m.title.Value() != "Title" {
		t.Fatal("title or scope input failed")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	m = next.(model)
	if m.body.Value() != "line one\nline two" {
		t.Fatalf("multiline input: %q", m.body.Value())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.mode != "browse" {
		t.Fatal("escape failed")
	}
}

func TestEditorSupportsLongBodiesAndWaitsForTransaction(t *testing.T) {
	m := newModel(app{store: store.Store{Root: t.TempDir()}, repo: syncgit.Repo{Root: t.TempDir()}})
	m.beginAdd()
	m.mode = "body"
	m.body.Focus()
	for range 120 {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(model)
	}
	if strings.Count(m.body.Value(), "\n") != 120 {
		t.Fatal("multiline editor stopped accepting newlines")
	}
	m.mode = "browse"
	m.busy = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(model)
	if cmd != nil || !m.quitting {
		t.Fatal("quit interrupted an active transaction")
	}
	_, cmd = m.Update(resultMsg{})
	if cmd == nil {
		t.Fatal("did not exit after transaction finished")
	}
}

func TestEditorScopeSwitchAndSave(t *testing.T) {
	for _, mode := range []string{"title", "body"} {
		for _, kind := range []string{"new", "global", "project", "other-project"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
				t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
				home := filepath.Join(t.TempDir(), "library")
				a := app{store: store.Store{Root: home}, repo: syncgit.Repo{Root: home}, project: "github.com/acme/api"}
				if kind == "other-project" {
					a.project = ""
				}
				if err := a.repo.Locked(func() error {
					if err := a.repo.Init(); err != nil {
						return err
					}
					return a.repo.MarkConfigured()
				}); err != nil {
					t.Fatal(err)
				}
				original, _ := store.New("Review", "First line\nSecond line", "")
				original.LastUsed = original.Created
				if kind == "project" {
					original.Project = a.project
				}
				if kind == "other-project" {
					original.Project = "github.com/other/api"
				}
				if kind != "new" {
					if err := a.store.Save(original); err != nil {
						t.Fatal(err)
					}
				}
				m := newModel(a)
				m.busy = false
				m.all = true
				m.prompts, _, _ = a.store.List("", "", true)
				key := "e"
				if kind == "new" {
					key = "a"
				}
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
				m = next.(model)
				if kind == "new" {
					m.title.SetValue(original.Title)
					m.body.SetValue(original.Body)
				}
				if mode == "body" {
					next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
					m = next.(model)
				}
				next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
				m = next.(model)
				wantProject := kind == "new" || kind == "global"
				if m.projectScope != wantProject {
					t.Fatal("Tab did not switch scope")
				}
				label := "Global"
				if wantProject {
					label = a.project
				}
				if !strings.Contains(m.View(), "Scope: "+label) {
					t.Fatalf("incorrect scope label: %s", m.View())
				}
				next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
				m = next.(model)
				if cmd == nil {
					t.Fatal("save did not start")
				}
				result := cmd().(resultMsg)
				if result.err != nil {
					t.Fatal(result.err)
				}
				ps, warnings, err := a.store.List("", "", true)
				if err != nil || len(warnings) > 0 || len(ps) != 1 {
					t.Fatalf("scope move left duplicate or invalid records: %+v %v %v", ps, warnings, err)
				}
				p := ps[0]
				scope := ""
				if wantProject {
					scope = a.project
				}
				if p.Project != scope || p.Title != original.Title || p.Body != original.Body {
					t.Fatalf("unexpected saved prompt: %+v", p)
				}
				if kind != "new" && (p.ID != original.ID || !p.Created.Equal(original.Created) || !p.LastUsed.Equal(original.LastUsed)) {
					t.Fatal("scope move changed prompt identity or usage")
				}
				if kind != "new" {
					if _, err := os.Stat(a.store.Path(original)); !os.IsNotExist(err) {
						t.Fatal("old scope file remains")
					}
				}
			})
		}
	}
}

func TestEditorExplainsUnavailableProjectScope(t *testing.T) {
	for _, mode := range []string{"title", "body"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			m := newModel(app{store: store.Store{Root: home}, repo: syncgit.Repo{Root: home}})
			m.beginAdd()
			if mode == "body" {
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				m = next.(model)
			}
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(model)
			if m.projectScope || !strings.Contains(m.status, "Git remote") || !strings.Contains(m.View(), "project needs a Git remote") {
				t.Fatal("missing explanation for unavailable scope")
			}
		})
	}
}
