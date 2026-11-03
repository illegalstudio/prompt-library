package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/illegalstudio/prompt-library/internal/clipboard"
	"github.com/illegalstudio/prompt-library/internal/store"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
var selected = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)

type resultMsg struct {
	prompts []store.Prompt
	status  string
	err     error
	manual  string
	done    bool
}
type model struct {
	a              app
	mode           string
	prompts        []store.Prompt
	cursor         int
	all            bool
	search         textinput.Model
	title          textinput.Model
	remote         textinput.Model
	body           textarea.Model
	manual         viewport.Model
	viewer         viewport.Model
	viewing        *store.Prompt
	darkBackground bool
	projectScope   bool
	editing        *store.Prompt
	deleting       *store.Prompt
	busy           bool
	quitting       bool
	status         string
	width, height  int
}

func newModel(a app) model {
	search := textinput.New()
	search.Placeholder = "Search title and body"
	search.CharLimit = 500
	title := textinput.New()
	title.Placeholder = "Give this prompt a title"
	title.CharLimit = 200
	remote := textinput.New()
	remote.Placeholder = "git@github.com:you/prompts.git (blank for local only)"
	remote.CharLimit = 2048
	body := textarea.New()
	body.Placeholder = "Write or paste your prompt here. Newlines are preserved."
	body.CharLimit = 0
	body.MaxHeight = 0
	body.ShowLineNumbers = false
	body.SetHeight(10)
	m := model{a: a, mode: "browse", search: search, title: title, remote: remote, body: body, width: 80, height: 24, manual: viewport.New(74, 12), viewer: viewport.New(76, 14), darkBackground: lipgloss.HasDarkBackground()}
	m.viewer.SetHorizontalStep(4)
	m.prompts, _, _ = a.store.List(a.project, "", false)
	if !a.repo.Configured() {
		m.mode = "setup"
		m.remote.Focus()
	} else {
		m.busy = true
		m.status = "Syncing..."
	}
	return m
}
func (m model) Init() tea.Cmd {
	if m.mode == "setup" {
		return textinput.Blink
	}
	return tea.Batch(textinput.Blink, m.load(true))
}
func (m model) load(sync bool) tea.Cmd {
	return func() tea.Msg {
		status := "Ready"
		var syncErr error
		if sync {
			syncErr = m.a.repo.Locked(m.a.repo.Sync)
			if syncErr != nil {
				status = "Sync pending: " + syncErr.Error()
			}
		}
		ps, warnings, err := m.a.store.List(m.a.project, m.search.Value(), m.all)
		if len(warnings) > 0 {
			status = strings.Join(warnings, "; ")
		}
		return resultMsg{prompts: ps, status: status, err: err}
	}
}
func (m *model) beginAdd() {
	m.mode = "title"
	m.editing = nil
	m.projectScope = false
	m.title.SetValue("")
	m.body.SetValue("")
	m.title.Focus()
	m.body.Blur()
	m.status = ""
}

func (m model) scopeProject() string {
	if m.editing != nil && m.editing.Project != "" {
		return m.editing.Project
	}
	return m.a.project
}

func (m model) listPageSize() int {
	rows := m.height - 11
	if m.mode == "search" || m.search.Value() != "" {
		rows -= 2
	}
	return max(1, rows)
}

func (m model) promptRow(p store.Prompt, active bool) string {
	width := max(1, m.width-4)
	titleWidth := min(30, max(1, width/3))
	scopeWidth := 16
	label := scopeLabel(p)
	if p.Project != "" {
		label = p.Project[strings.LastIndex(p.Project, "/")+1:]
	}
	if width < 60 {
		scopeWidth = 3
		label = "G"
		if p.Project != "" {
			label = "P"
		}
	}
	if width < 28 {
		scopeWidth = 0
	}
	compact := func(s string, limit int) string {
		tail := "..."
		if limit < len(tail) {
			tail = ""
		}
		return ansi.Truncate(strings.Join(strings.Fields(s), " "), limit, tail)
	}
	style := lipgloss.NewStyle()
	prefix := "  "
	if active {
		style = selected
		prefix = "> "
	}
	if width < 5 {
		return "  " + prefix + style.Render(compact(p.Title, width))
	}
	columns := []string{style.Width(titleWidth).Render(compact(p.Title, titleWidth))}
	bodyWidth := width - titleWidth - 2
	if scopeWidth > 0 {
		scope := "[" + compact(label, scopeWidth-2) + "]"
		columns = append(columns, muted.Width(scopeWidth).Render(scope))
		bodyWidth -= scopeWidth + 2
	}
	columns = append(columns, muted.Render(compact(p.Body, bodyWidth)))
	return "  " + prefix + strings.Join(columns, "  ")
}

func (m *model) resize() {
	w := max(20, m.width-6)
	m.title.Width = w
	m.search.Width = w
	m.remote.Width = w
	m.body.SetWidth(w)
	m.body.SetHeight(max(3, m.height-12))
	m.manual.Width = w
	m.manual.Height = max(3, m.height-9)
	m.viewer.Width = max(1, m.width-4)
	m.viewer.Height = max(1, m.height-10)
	if m.viewing != nil {
		m.renderViewer()
	}
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil
	case resultMsg:
		m.busy = false
		if m.quitting {
			return m, tea.Quit
		}
		if msg.manual != "" {
			m.mode = "manual"
			m.manual.SetContent(msg.manual)
			m.manual.GotoTop()
		}
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.prompts = msg.prompts
		m.cursor = min(m.cursor, max(0, len(m.prompts)-1))
		m.status = msg.status
		if msg.done {
			m.mode = "browse"
			m.title.Blur()
			m.body.Blur()
		}
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			if m.busy {
				m.quitting = true
				m.status = "Finishing Git transaction before exit..."
				return m, nil
			}
			return m, tea.Quit
		}
		if m.mode == "setup" {
			if key == "esc" {
				if m.busy {
					m.quitting = true
					return m, nil
				}
				return m, tea.Quit
			}
			if key == "enter" && !m.busy {
				m.busy = true
				m.status = "Preparing library..."
				url := strings.TrimSpace(m.remote.Value())
				return m, func() tea.Msg {
					var syncErr error
					err := m.a.repo.Locked(func() error {
						if url != "" {
							if err := m.a.repo.SetRemote(url); err != nil {
								return err
							}
						}
						if err := m.a.repo.MarkConfigured(); err != nil {
							return err
						}
						syncErr = m.a.repo.Sync()
						return nil
					})
					if err != nil {
						return resultMsg{err: err}
					}
					ps, warnings, err := m.a.store.List(m.a.project, "", false)
					status := "Library ready. Press a to add your first prompt."
					if syncErr != nil {
						status = "Local library ready; sync pending: " + syncErr.Error()
					}
					if len(warnings) > 0 {
						status = strings.Join(warnings, "; ")
					}
					return resultMsg{prompts: ps, status: status, err: err, done: true}
				}
			}
			var cmd tea.Cmd
			m.remote, cmd = m.remote.Update(msg)
			return m, cmd
		}
		if m.mode == "manual" {
			if key == "esc" || key == "enter" {
				m.mode = "browse"
				return m, nil
			}
			var cmd tea.Cmd
			m.manual, cmd = m.manual.Update(msg)
			return m, cmd
		}
		if m.mode == "view" {
			switch key {
			case "esc", "q", "v":
				m.mode = "browse"
				m.viewing = nil
				return m, nil
			case "home", "g":
				m.viewer.GotoTop()
				return m, nil
			case "end", "G":
				m.viewer.GotoBottom()
				return m, nil
			case "enter":
				// Use the list's copy action so clipboard and fallback keep the exact source.
				m.mode = "browse"
				m.viewing = nil
			default:
				var cmd tea.Cmd
				m.viewer, cmd = m.viewer.Update(msg)
				return m, cmd
			}
		}
		if m.mode == "delete" {
			switch key {
			case "y", "Y":
				return m.deletePrompt()
			case "n", "N", "enter", "esc":
				m.mode = "browse"
				m.deleting = nil
				m.status = "Deletion cancelled"
			}
			return m, nil
		}
		if m.mode == "title" || m.mode == "body" {
			if m.busy {
				return m, nil
			}
			switch key {
			case "esc":
				m.mode = "browse"
				m.title.Blur()
				m.body.Blur()
				m.status = "Draft discarded"
				return m, nil
			case "tab":
				if m.scopeProject() == "" {
					m.status = "Project scope unavailable: add a Git remote to the current project, then restart plib."
					return m, nil
				}
				m.projectScope = !m.projectScope
				m.status = ""
				return m, nil
			case "enter":
				if m.mode == "title" {
					m.mode = "body"
					m.title.Blur()
					return m, m.body.Focus()
				}
			case "ctrl+s":
				if !m.busy {
					return m.save()
				}
				return m, nil
			case "ctrl+t":
				m.mode = "title"
				m.body.Blur()
				return m, m.title.Focus()
			}
			var cmd tea.Cmd
			if m.mode == "title" {
				m.title, cmd = m.title.Update(msg)
			} else {
				m.body, cmd = m.body.Update(msg)
			}
			return m, cmd
		}
		if m.mode == "search" {
			if key == "esc" || key == "enter" {
				m.mode = "browse"
				m.search.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			m.cursor = 0
			// Filtering local files is quick and synchronous, avoiding stale asynchronous search results.
			ps, _, err := m.a.store.List(m.a.project, m.search.Value(), m.all)
			if err == nil {
				m.prompts = ps
			}
			return m, cmd
		}
		switch key {
		case "q", "esc":
			if m.busy {
				m.quitting = true
				m.status = "Finishing Git transaction before exit..."
				return m, nil
			}
			return m, tea.Quit
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(max(0, len(m.prompts)-1), m.cursor+1)
		case "pgdown":
			m.cursor = min(max(0, len(m.prompts)-1), m.cursor+m.listPageSize())
		case "pgup":
			m.cursor = max(0, m.cursor-m.listPageSize())
		case "s", "/":
			if m.busy {
				return m, nil
			}
			m.mode = "search"
			return m, m.search.Focus()
		case "a":
			if !m.busy {
				m.beginAdd()
				return m, textinput.Blink
			}
		case "e":
			if !m.busy && len(m.prompts) > 0 {
				p := m.prompts[m.cursor]
				m.beginAdd()
				m.editing = &p
				m.title.SetValue(p.Title)
				m.body.SetValue(p.Body)
				m.projectScope = p.Project != ""
				return m, textinput.Blink
			}
		case "v":
			if !m.busy && len(m.prompts) > 0 {
				p := m.prompts[m.cursor]
				m.viewing = &p
				m.mode = "view"
				m.renderViewer()
				m.viewer.GotoTop()
			}
		case "tab":
			if m.busy {
				return m, nil
			}
			m.all = !m.all
			m.cursor = 0
			ps, _, err := m.a.store.List(m.a.project, m.search.Value(), m.all)
			if err != nil {
				m.status = err.Error()
			} else {
				m.prompts = ps
			}
			return m, nil
		case "d":
			if !m.busy && len(m.prompts) > 0 {
				p := m.prompts[m.cursor]
				m.deleting = &p
				m.mode = "delete"
				m.status = ""
			}
		case "r":
			if !m.busy {
				m.busy = true
				m.status = "Syncing..."
				return m, m.load(true)
			}
		case "enter":
			if !m.busy && len(m.prompts) > 0 {
				p := m.prompts[m.cursor]
				m.busy = true
				m.status = "Copying..."
				return m, func() tea.Msg {
					clipErr := clipboard.Write(p.Body)
					var syncErr error
					err := m.a.repo.Locked(func() error {
						current, err := m.a.store.Find(p.ID)
						if err != nil {
							return err
						}
						current.LastUsed = time.Now().UTC()
						if err := m.a.store.Save(current); err != nil {
							return err
						}
						syncErr = m.a.repo.Sync()
						return nil
					})
					ps, _, listErr := m.a.store.List(m.a.project, m.search.Value(), m.all)
					if err == nil {
						err = listErr
					}
					status := "Copied: " + p.Title
					if syncErr != nil {
						status += "; sync pending: " + syncErr.Error()
					}
					manual := ""
					if clipErr != nil {
						status = "Clipboard unavailable. Copy the prompt manually."
						manual = p.Body
					}
					return resultMsg{prompts: ps, status: status, err: err, manual: manual}
				}
			}
		}
	}
	// Forward cursor blink and textarea paste messages to the focused input.
	var cmd tea.Cmd
	switch m.mode {
	case "setup":
		m.remote, cmd = m.remote.Update(msg)
	case "title":
		m.title, cmd = m.title.Update(msg)
	case "body":
		m.body, cmd = m.body.Update(msg)
	case "search":
		m.search, cmd = m.search.Update(msg)
	case "view":
		m.viewer, cmd = m.viewer.Update(msg)
	}
	return m, cmd
}

func (m model) deletePrompt() (tea.Model, tea.Cmd) {
	if m.deleting == nil || m.busy {
		return m, nil
	}
	p := *m.deleting
	m.mode = "browse"
	m.deleting = nil
	m.busy = true
	m.status = "Deleting..."
	return m, func() tea.Msg {
		var syncErr error
		err := m.a.repo.Locked(func() error {
			current, err := m.a.store.Find(p.ID)
			if err != nil {
				return err
			}
			if current.Title != p.Title || current.Body != p.Body || current.Project != p.Project {
				return fmt.Errorf("prompt changed in another process; select it again before deleting")
			}
			if err := m.a.store.Delete(current); err != nil {
				return err
			}
			syncErr = m.a.repo.Sync()
			return nil
		})
		if err != nil {
			return resultMsg{err: err}
		}
		ps, _, err := m.a.store.List(m.a.project, m.search.Value(), m.all)
		status := "Deleted: " + p.Title
		if _, findErr := m.a.store.Find(p.ID); findErr == nil {
			status = "Prompt changed during sync and was kept; review it before deleting again"
		}
		if syncErr != nil {
			status += "; sync pending: " + syncErr.Error()
		}
		return resultMsg{prompts: ps, status: status, err: err, done: true}
	}
}

func (m model) save() (tea.Model, tea.Cmd) {
	scope := ""
	if m.projectScope {
		scope = m.scopeProject()
	}
	p, err := store.New(m.title.Value(), m.body.Value(), scope)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	if m.editing != nil {
		p.ID = m.editing.ID
		p.Created = m.editing.Created
		p.LastUsed = m.editing.LastUsed
	}
	m.busy = true
	m.status = "Saving..."
	return m, func() tea.Msg {
		var syncErr error
		err := m.a.repo.Locked(func() error {
			// Preserve concurrent edits before replacing an existing prompt.
			if m.editing != nil {
				current, e := m.a.store.Find(p.ID)
				if e != nil {
					return e
				}
				if current.Title != m.editing.Title || current.Body != m.editing.Body || current.Project != m.editing.Project {
					return fmt.Errorf("prompt changed in another process; reopen it before editing")
				}
				p.LastUsed = current.LastUsed
				if err := m.a.store.Replace(current, p); err != nil {
					return err
				}
			} else if err := m.a.store.Save(p); err != nil {
				return err
			}
			syncErr = m.a.repo.Sync()
			return nil
		})
		if err != nil {
			return resultMsg{err: err}
		}
		ps, _, err := m.a.store.List(m.a.project, m.search.Value(), m.all)
		status := "Saved: " + p.Title
		if syncErr != nil {
			status += "; sync pending: " + syncErr.Error()
		}
		return resultMsg{prompts: ps, status: status, err: err, done: true}
	}
}
func (m model) View() string {
	var b strings.Builder
	b.WriteString(accent.Render("  plib") + muted.Render("  your prompts, at hand") + "\n\n")
	if m.mode == "setup" {
		b.WriteString("  Welcome. Your library lives at " + m.a.store.Root + "\n\n  Git remote (optional)\n  " + m.remote.View() + "\n\n" + muted.Render("  Enter: continue  |  Leave blank: local only  |  Esc: quit"))
	} else if m.mode == "title" || m.mode == "body" {
		label := "Global"
		if m.projectScope {
			label = m.scopeProject()
		}
		scopeNote := ""
		if m.scopeProject() == "" {
			scopeNote = muted.Render(" (project needs a Git remote)")
		}
		b.WriteString("  Scope: " + accent.Render(label) + scopeNote + "\n\n  Title\n  " + m.title.View() + "\n\n" + m.body.View() + "\n" + muted.Render("  Enter: next field / newline  |  Tab: scope  |  Ctrl+S: save\n  Ctrl+T: title  |  Esc: discard"))
	} else if m.mode == "manual" {
		b.WriteString("  Clipboard unavailable. Select and copy the text manually.\n\n" + m.manual.View() + "\n\n" + muted.Render("  Up/Down: scroll  |  Enter/Esc: back"))
	} else if m.mode == "view" && m.viewing != nil {
		b.WriteString(m.viewerView())
	} else {
		scope := "Global"
		if m.a.project != "" {
			scope += " + " + m.a.project
		}
		if m.all {
			scope = "All projects"
		}
		b.WriteString("  " + muted.Render(scope) + fmt.Sprintf("  (%d prompts)\n", len(m.prompts)))
		if m.mode == "search" || m.search.Value() != "" {
			b.WriteString("\n  " + m.search.View() + "\n\n")
		} else {
			b.WriteString("\n")
		}
		rows := m.listPageSize()
		start := 0
		if m.cursor >= rows {
			start = m.cursor - rows + 1
		}
		for i := start; i < min(len(m.prompts), start+rows); i++ {
			b.WriteString(m.promptRow(m.prompts[i], i == m.cursor) + "\n")
		}
		if len(m.prompts) == 0 {
			b.WriteString("  No prompts yet. Press a to add one.\n")
		}
		if m.mode == "delete" && m.deleting != nil {
			b.WriteString(fmt.Sprintf("\n  Delete %q? [y/N]\n", preview(m.deleting.Title, max(5, m.width-25))))
			b.WriteString(muted.Render("  y: delete  |  N / Enter / Esc: cancel"))
		} else {
			b.WriteString("\n" + muted.Render("  a add  |  s search  |  Enter copy  |  v view  |  e edit  |  d delete\n  Tab all/current  |  r sync  |  Up/Down select  |  q quit"))
		}
	}
	if m.status != "" {
		b.WriteString("\n\n  " + muted.Render(preview(m.status, max(20, m.width-4))))
	}
	return b.String() + "\n"
}
