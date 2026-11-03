package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/illegalstudio/prompt-library/internal/store"
)

const markdownExample = "# Review\n\nCheck **correctness** and *clarity*.\n\n- First task\n- [x] Done\n\n[Website](https://example.com)\n\n| Area | State |\n| --- | --- |\n| Tests | Ready |\n\n```go\nfmt.Println(\"hello\")\n```\n"

func TestMarkdownRendersFormatting(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			rendered, err := renderMarkdown(markdownExample, 76, dark)
			if err != nil {
				t.Fatal(err)
			}
			plain := ansi.Strip(rendered)
			for _, text := range []string{"Review", "correctness", "clarity", "First task", "Done", "https://example.com", "Area", "Tests", `fmt.Println("hello")`} {
				if !strings.Contains(plain, text) {
					t.Fatalf("rendered Markdown lost %q: %q", text, plain)
				}
			}
			for _, syntax := range []string{"**correctness**", "*clarity*", "```go", "[Website]("} {
				if strings.Contains(plain, syntax) {
					t.Fatalf("Markdown syntax was not rendered: %q", syntax)
				}
			}
		})
	}
}

func TestMarkdownViewPreservesSourceAndListState(t *testing.T) {
	m, p := deleteModel(t, "")
	p.Title = "Markdown note"
	p.Body = markdownExample
	p.LastUsed = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := m.a.repo.Locked(func() error { return m.a.store.Save(p) }); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.a.store.Path(p))
	if err != nil {
		t.Fatal(err)
	}
	other := p
	other.Title = "Other note"
	m.prompts = append(m.prompts[:0], other, p)
	m.cursor = 1
	m.search.SetValue("note")
	m.all = true
	m.status = "Ready"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(model)
	if cmd != nil || m.mode != "view" || !strings.Contains(ansi.Strip(m.View()), "Markdown note") || strings.Contains(ansi.Strip(m.View()), "**correctness**") {
		t.Fatal("v did not open a rendered, read-only preview")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyRunes, Runes: []rune("j")}, {Type: tea.KeyPgDown}} {
		next, _ = m.Update(key)
		m = next.(model)
	}
	for _, back := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyRunes, Runes: []rune("v")}} {
		current := m
		next, cmd = current.Update(back)
		current = next.(model)
		if cmd != nil || current.mode != "browse" || current.cursor != 1 || current.search.Value() != "note" || !current.all || current.viewing != nil {
			t.Fatalf("%s did not restore the list and its filter", back.String())
		}
	}
	after, err := os.ReadFile(m.a.store.Path(p))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("viewing changed the stored source or usage metadata")
	}
	if m.prompts[1].Body != markdownExample || m.viewing.Body != markdownExample {
		t.Fatal("rendering replaced the source text")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.busy || m.mode != "browse" || m.prompts[m.cursor].Body != markdownExample {
		t.Fatal("Enter did not delegate to the original Markdown copy action")
	}
}

func TestMarkdownViewScrollsAndReflowsOnResize(t *testing.T) {
	m, p := deleteModel(t, "")
	p.Body = "# Long note\n\n" + strings.Repeat("A paragraph with enough words to reflow when the terminal becomes narrower.\n\n", 40)
	m.prompts = []store.Prompt{p}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(model)
	before := m.viewer.TotalLineCount()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.viewer.YOffset == 0 || m.cursor != 0 {
		t.Fatal("PageDown did not scroll the note independently of the list")
	}
	offset := m.viewer.YOffset
	next, _ = m.Update(tea.WindowSizeMsg{Width: 38, Height: 20})
	m = next.(model)
	if m.viewer.TotalLineCount() <= before || m.viewer.YOffset != offset {
		t.Fatal("resize did not reflow the note and retain the scroll position")
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if ansi.StringWidth(line) > 38 {
			t.Fatalf("viewer overflowed a narrow terminal: %q", line)
		}
	}
	if lines := strings.Count(m.View(), "\n"); lines > 20 {
		t.Fatalf("viewer overflowed the terminal height: %d", lines)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(model)
	if !m.viewer.AtBottom() {
		t.Fatal("End did not show the end of the note")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(model)
	if !m.viewer.AtTop() {
		t.Fatal("Home did not show the start of the note")
	}
	// Long code is wrapped by the renderer without losing its end.
	m.viewing.Body = "```text\n" + strings.Repeat("x", 100) + " TAIL\n```"
	m.renderViewer()
	m.viewer.GotoBottom()
	if !strings.Contains(ansi.Strip(m.viewer.View()), "TAIL") {
		t.Fatal("long code lost its end after wrapping")
	}
	// Any rendered line wider than the viewport can also be panned.
	m.viewer.SetContent(strings.Repeat("x", 100) + " TAIL")
	m.viewer.GotoTop()
	m.viewer.SetXOffset(0)
	for range 30 {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = next.(model)
	}
	if !strings.Contains(ansi.Strip(m.viewer.View()), "TAIL") {
		t.Fatal("horizontal scrolling hid the end of a wide line")
	}
}

func TestViewKeyDoesNotHijackInputsOrBusyState(t *testing.T) {
	m, _ := deleteModel(t, "")
	for _, state := range []string{"empty", "busy", "search", "title", "body"} {
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
			default:
				current.beginAdd()
				if state == "body" {
					current.mode = "body"
					current.body.Focus()
				}
			}
			next, _ := current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
			current = next.(model)
			if current.mode == "view" || current.viewing != nil {
				t.Fatal("v opened a view outside an idle nonempty list")
			}
			if state == "search" && current.search.Value() != "v" || state == "title" && current.title.Value() != "v" || state == "body" && current.body.Value() != "v" {
				t.Fatal("v was not inserted into the focused input")
			}
		})
	}
}
