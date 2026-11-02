package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/illegalstudio/prompt-library/skills"
)

type skillTarget struct{ Key, Path string }

func skillTargets(home string) []skillTarget {
	return []skillTarget{{"agents", skills.AgentSkillsInstallPath(home)}, {"claude", skills.ClaudeSkillsInstallPath(home)}}
}
func filterSkillTargets(all []skillTarget, requested []string) ([]skillTarget, error) {
	if len(requested) == 0 {
		return all, nil
	}
	wanted := map[string]bool{}
	for _, key := range requested {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if key != "agents" && key != "claude" {
			return nil, fmt.Errorf("unknown skill target %q (valid targets: agents, claude; repeat --target for multiple destinations)", key)
		}
		wanted[key] = true
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("no skill target selected")
	}
	var result []skillTarget
	for _, target := range all {
		if wanted[target.Key] {
			result = append(result, target)
		}
	}
	return result, nil
}

type skillResultItem struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}
type skillReport struct {
	Name          string            `json:"name"`
	Installations []skillResultItem `json:"installations,omitempty"`
	Verifications []skillResultItem `json:"verifications,omitempty"`
}

func newSkillsCommand() *cobra.Command {
	var asJSON bool
	group := &cobra.Command{
		Use: "skills", Short: "Manage the bundled AI agent skill", Args: cobra.NoArgs,
		// Skill management does not initialize or synchronize the prompt library.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	group.PersistentFlags().BoolVar(&asJSON, "json", false, "Print a structured report without prompting")
	for _, action := range []string{"install", "verify"} {
		action := action
		var requested []string
		var force bool
		child := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action == "install" {
			child.Short = "Install or update the plib skill for agents and Claude Code"
			child.Long = "Install the skill bundled with this binary. Destinations: agents (~/.agents/skills/plib) and claude (~/.claude/skills/plib). Without --target, a terminal menu preselects both. With --json, both are selected without prompting. Modified or unmanaged copies require --force."
			child.Flags().BoolVar(&force, "force", false, "Replace an installed skill that differs from the bundled version")
		} else {
			child.Short = "Check installed skills against the running binary"
			child.Long = "Check both destinations, or only those selected with --target. Missing copies are reported without error. Outdated, modified or unreadable copies cause a nonzero exit status. Verification never prompts or writes."
			child.Aliases = []string{"list", "status"}
		}
		child.Flags().StringArrayVar(&requested, "target", nil, "Choose agents or claude; repeatable, not comma-separated")
		_ = child.RegisterFlagCompletionFunc("target", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			var keys []string
			for _, key := range []string{"agents", "claude"} {
				if strings.HasPrefix(key, toComplete) {
					keys = append(keys, key)
				}
			}
			return keys, cobra.ShellCompDirectiveNoFileComp
		})
		child.RunE = func(cmd *cobra.Command, args []string) error {
			fail := func(err error) error {
				if asJSON {
					if e := json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"error": err.Error()}); e != nil {
						return e
					}
				}
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return fail(err)
			}
			targets, err := filterSkillTargets(skillTargets(home), requested)
			if err != nil {
				return fail(err)
			}
			if action == "install" && len(requested) == 0 && !asJSON {
				targets, err = promptSkillTargets(cmd, targets)
				if err != nil {
					return fail(err)
				}
			}
			report := skillReport{Name: skills.Name}
			failed, modified := false, false
			for _, target := range targets {
				item := skillResultItem{Target: target.Key, Path: target.Path}
				if action == "install" {
					status, e := skills.Install(target.Path, force)
					item.Status = string(status)
					err = e
				} else {
					status, e := skills.Verify(target.Path)
					item.Status = string(status)
					err = e
					if status.Stale() {
						failed = true
					}
					if status == skills.VerifyModified {
						modified = true
					}
				}
				if err != nil {
					item.Error = err.Error()
					failed = true
				}
				if action == "install" {
					report.Installations = append(report.Installations, item)
				} else {
					report.Verifications = append(report.Verifications, item)
				}
				if !asJSON {
					status := item.Status
					if item.Error != "" {
						status = item.Error
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", accent.Render(item.Target), muted.Render(status), item.Path)
				}
			}
			if asJSON {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(report); err != nil {
					return err
				}
			}
			if failed {
				if action == "install" {
					return fmt.Errorf("one or more skill installations failed; see the per-destination results")
				}
				advice := "run plib skills install to update managed copies"
				if modified {
					advice += "; locally modified copies require --force to replace"
				}
				return fmt.Errorf("skill verification failed: %s", advice)
			}
			if action == "install" && !asJSON {
				fmt.Fprintln(cmd.OutOrStdout(), "Restart your agent if the skill does not appear automatically.")
			}
			return nil
		}
		group.AddCommand(child)
	}
	return group
}

func interactiveCommand(cmd *cobra.Command) bool {
	input, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) {
		return false
	}
	output, ok := cmd.OutOrStdout().(*os.File)
	return ok && term.IsTerminal(int(output.Fd()))
}

type skillPicker struct {
	targets   []skillTarget
	chosen    []bool
	cursor    int
	confirmed bool
}

func (m skillPicker) Init() tea.Cmd { return nil }
func (m skillPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(len(m.targets)-1, m.cursor+1)
		case " ":
			m.chosen[m.cursor] = !m.chosen[m.cursor]
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m skillPicker) View() string {
	var b strings.Builder
	b.WriteString(accent.Render("Install the plib agent skill") + "\n\n")
	for i, target := range m.targets {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		check := "[ ]"
		if m.chosen[i] {
			check = "[x]"
		}
		fmt.Fprintf(&b, "%s%s %s  %s\n", cursor, check, target.Key, muted.Render(target.Path))
	}
	b.WriteString("\nSpace: toggle  |  Enter: install  |  Esc: cancel\n")
	return b.String()
}
func promptSkillTargets(cmd *cobra.Command, all []skillTarget) ([]skillTarget, error) {
	if !interactiveCommand(cmd) {
		return nil, fmt.Errorf("skill destination selection requires a terminal; pass --target agents, --target claude or --json")
	}
	picker := skillPicker{targets: all, chosen: make([]bool, len(all))}
	for i := range picker.chosen {
		picker.chosen[i] = true
	}
	result, err := tea.NewProgram(picker, tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout())).Run()
	if err != nil {
		return nil, err
	}
	selected := result.(skillPicker)
	if !selected.confirmed {
		return nil, fmt.Errorf("skill installation cancelled")
	}
	var targets []skillTarget
	for i, target := range all {
		if selected.chosen[i] {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no skill target selected")
	}
	return targets, nil
}

func maybeNoticeStaleSkills(cmd *cobra.Command) {
	if !interactiveCommand(cmd) || os.Getenv("PLIB_SUPPRESS_SKILLS_NOTICE") == "1" {
		return
	}
	for parent := cmd; parent != nil; parent = parent.Parent() {
		if parent.Hidden || parent.Name() == "skills" || parent.Name() == "completion" || parent.Name() == "show" {
			return
		}
	}
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, target := range skillTargets(home) {
		status, err := skills.Verify(target.Path)
		if err != nil || !status.Stale() {
			continue
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "note: plib agent skill at %s is %s; run plib skills verify and plib skills install to update. Modified files need --force to replace. Set PLIB_SUPPRESS_SKILLS_NOTICE=1 to silence this notice.\n", target.Path, status)
	}
}
