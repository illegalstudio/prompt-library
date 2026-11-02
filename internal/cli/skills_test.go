package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/illegalstudio/prompt-library/skills"
)

func isolatedSkillHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home, filepath.Join(home, "prompt-data")
}
func decodeSkillReport(t *testing.T, out string) skillReport {
	t.Helper()
	var result skillReport
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid skill report %q: %v", out, err)
	}
	return result
}
func assertNoPromptData(t *testing.T, path string) {
	t.Helper()
	for _, p := range []string{path, path + ".lock"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("skill command created prompt data at %s", p)
		}
	}
}
func TestSkillCommandsWorkWithoutGitOrPromptSetup(t *testing.T) {
	home, library := isolatedSkillHome(t)
	t.Setenv("PATH", home)
	out, _, err := run(t, library, "", "skills", "verify", "--json")
	if err != nil {
		t.Fatal(err)
	}
	report := decodeSkillReport(t, out)
	if report.Name != "plib" || len(report.Verifications) != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	for _, item := range report.Verifications {
		if item.Status != "not-installed" {
			t.Fatal(item)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatal("verification wrote skill files")
	}
	for i, want := range []string{"installed", "up-to-date"} {
		out, _, err = run(t, library, "", "skills", "install", "--json")
		if err != nil {
			t.Fatal(err)
		}
		report = decodeSkillReport(t, out)
		if len(report.Installations) != 2 {
			t.Fatal(report)
		}
		for _, item := range report.Installations {
			if item.Status != want {
				t.Fatalf("run %d: %+v", i, item)
			}
		}
	}
	for _, target := range skillTargets(home) {
		if _, err := os.Stat(filepath.Join(target.Path, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
	assertNoPromptData(t, library)
}
func TestSkillsProtectModifiedFilesAndReportPartialFailures(t *testing.T) {
	home, library := isolatedSkillHome(t)
	if _, err := skills.Install(skills.AgentSkillsInstallPath(home), false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skills.AgentSkillsInstallPath(home), "SKILL.md")
	if err := os.WriteFile(path, []byte("local changes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, library, "", "skills", "verify", "--json")
	if err == nil {
		t.Fatal("modified skills must fail verification")
	}
	report := decodeSkillReport(t, out)
	if len(report.Verifications) != 2 || report.Verifications[0].Status != "modified" || report.Verifications[1].Status != "not-installed" {
		t.Fatal(report)
	}
	out, _, err = run(t, library, "", "skills", "install", "--json")
	if err == nil {
		t.Fatal("a rejected destination must return a failure")
	}
	report = decodeSkillReport(t, out)
	if len(report.Installations) != 2 || report.Installations[0].Error == "" || report.Installations[1].Status != "installed" {
		t.Fatal(report)
	}
	if body, _ := os.ReadFile(path); string(body) != "local changes\n" {
		t.Fatal("install overwrote local changes")
	}
	out, _, err = run(t, library, "", "skills", "install", "--target", "agents", "--force", "--json")
	if err != nil {
		t.Fatal(err)
	}
	report = decodeSkillReport(t, out)
	if len(report.Installations) != 1 || report.Installations[0].Status != "replaced" {
		t.Fatal(report)
	}
	out, _, err = run(t, library, "", "skills", "verify", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range decodeSkillReport(t, out).Verifications {
		if item.Status != "up-to-date" {
			t.Fatal(item)
		}
	}
	assertNoPromptData(t, library)
}
func TestSkillTargetsAndNoninteractiveSelection(t *testing.T) {
	home, library := isolatedSkillHome(t)
	if _, _, err := run(t, library, "", "skills", "install"); err == nil || !strings.Contains(err.Error(), "--target") {
		t.Fatal("noninteractive installation must request explicit destinations")
	}
	out, _, err := run(t, library, "", "skills", "install", "--target", "claude", "--target", " AGENTS ", "--target", "agents", "--json")
	if err != nil {
		t.Fatal(err)
	}
	report := decodeSkillReport(t, out)
	if len(report.Installations) != 2 || report.Installations[0].Target != "agents" || report.Installations[1].Target != "claude" {
		t.Fatal(report)
	}
	for _, target := range []string{"agents,claude", "unknown", " "} {
		out, _, err = run(t, library, "", "skills", "verify", "--target", target, "--json")
		if err == nil {
			t.Fatalf("invalid target %q accepted", target)
		}
		var message map[string]string
		if json.Unmarshal([]byte(out), &message) != nil || message["error"] == "" {
			t.Fatalf("missing structured command error: %s", out)
		}
	}
	out, _, err = run(t, library, "", "skills", "status", "--target", "claude", "--json")
	if err != nil || len(decodeSkillReport(t, out).Verifications) != 1 {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(skills.AgentSkillsInstallPath(home)); err != nil {
		t.Fatal(err)
	}
	assertNoPromptData(t, library)
}
func TestSkillTargetCompletionDoesNotInitializeLibrary(t *testing.T) {
	_, library := isolatedSkillHome(t)
	out, _, err := run(t, library, "", "__complete", "skills", "install", "--target", "a")
	if err != nil || !strings.Contains(out, "agents") || strings.Contains(out, "claude") {
		t.Fatalf("unexpected completion: %q %v", out, err)
	}
	assertNoPromptData(t, library)
}
func TestSkillPickerPreselectsBothAndAllowsDeselection(t *testing.T) {
	picker := skillPicker{targets: skillTargets(t.TempDir()), chosen: []bool{true, true}}
	next, _ := picker.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	picker = next.(skillPicker)
	if picker.chosen[0] || !picker.chosen[1] {
		t.Fatal("space did not toggle only the current destination")
	}
	next, cmd := picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	picker = next.(skillPicker)
	if !picker.confirmed || cmd == nil {
		t.Fatal("enter did not confirm the chosen destinations")
	}
}
