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
