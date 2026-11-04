package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/illegalstudio/prompt-library/internal/clipboard"
	"github.com/illegalstudio/prompt-library/internal/project"
	"github.com/illegalstudio/prompt-library/internal/store"
	"github.com/illegalstudio/prompt-library/internal/syncgit"
	"github.com/illegalstudio/prompt-library/internal/version"
)

type app struct {
	store   store.Store
	repo    syncgit.Repo
	project string
	out     io.Writer
	errOut  io.Writer
}

func Execute() error { return NewCommand().Execute() }

func NewCommand() *cobra.Command {
	var home string
	var a app
	root := &cobra.Command{
		Use: "plib", Short: "Project Library: your prompts, wherever you work",
		Version:      version.Version + " (" + version.Commit + ")",
		SilenceUsage: true, SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Hidden || cmd.Name() == "completion" || (cmd.Parent() != nil && cmd.Parent().Name() == "completion") {
				return nil
			}
			var err error
			if home == "" {
				home, err = store.DefaultRoot()
				if err != nil {
					return err
				}
			}
			home, err = filepath.Abs(home)
			if err != nil {
				return err
			}
			a = app{store: store.Store{Root: home}, repo: syncgit.Repo{Root: home}, out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr()}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			a.project, err = project.Identity(cwd)
			if err != nil {
				fmt.Fprintln(a.errOut, "Project not recognized; showing global prompts:", err)
				a.project = ""
			}
			return a.repo.Locked(a.repo.Init)
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) { maybeNoticeStaleSkills(cmd) },
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
				return errors.New("interactive mode requires a terminal; use plib init, add, list, show or copy")
			}
			_, err := tea.NewProgram(newModel(a), tea.WithAltScreen(), tea.WithOutput(cmd.OutOrStdout())).Run()
			return err
		},
		Args: cobra.NoArgs,
	}
	root.PersistentFlags().StringVar(&home, "home", "", "Library directory (default ~/.plib, or PLIB_HOME)")
	initCmd := &cobra.Command{Use: "init", Short: "Initialize local storage and optionally connect a remote", Args: cobra.NoArgs}
	var remote string
	var local bool
	initCmd.Flags().StringVar(&remote, "remote", "", "Git remote URL or path")
	initCmd.Flags().BoolVar(&local, "local", false, "Use local Git storage without a remote")
	initCmd.MarkFlagsMutuallyExclusive("local", "remote")
	initCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if remote == "" && !local {
			return errors.New("choose --local or --remote URL, or run plib for guided setup")
		}
		if err := a.repo.Locked(func() error {
			if a.repo.Configured() {
				return errors.New("library is already initialized; use plib remote set URL or plib remote remove")
			}
			if remote != "" {
				if err := a.repo.SetRemote(remote); err != nil {
					return err
				}
			}
			if err := a.repo.MarkConfigured(); err != nil {
				return err
			}
			a.warn(a.repo.Sync())
			return nil
		}); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Library ready:", a.store.Root)
		return nil
	}
	root.AddCommand(initCmd)
	listCmd := &cobra.Command{Use: "list [query]", Aliases: []string{"ls", "search"}, Short: "List or search titles and bodies, most recently used first", Args: cobra.MaximumNArgs(1)}
	var all, asJSON bool
	listCmd.Flags().BoolVar(&all, "all", false, "Include prompts from every project")
	listCmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	listCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := a.ready(); err != nil {
			return err
		}
		a.sync()
		query := ""
		if len(args) > 0 {
			query = args[0]
		}
		ps, warnings, err := a.store.List(a.project, query, all)
		if err != nil {
			return err
		}
		for _, w := range warnings {
			fmt.Fprintln(a.errOut, w)
		}
		if asJSON {
			return json.NewEncoder(a.out).Encode(ps)
		}
		for _, p := range ps {
			fmt.Fprintf(a.out, "%s  %s [%s]\n  %s\n", p.ID[:8], p.Title, scopeLabel(p), preview(p.Body, 90))
		}
		if len(ps) == 0 {
			fmt.Fprintln(a.out, "No prompts yet. Run plib and press a to add one.")
		}
		return nil
	}
	root.AddCommand(listCmd)
	var title, body string
	var global, projectOnly, stdin bool
	add := &cobra.Command{Use: "add", Short: "Add a prompt, or open the multiline form", Args: cobra.NoArgs}
	add.Flags().StringVar(&title, "title", "", "Prompt title")
	add.Flags().StringVar(&body, "body", "", "Prompt body")
	add.Flags().BoolVar(&stdin, "stdin", false, "Read the complete body from standard input")
	add.Flags().BoolVar(&global, "global", false, "Save globally (default)")
	add.Flags().BoolVar(&projectOnly, "project", false, "Save for the current Git remote")
	add.MarkFlagsMutuallyExclusive("global", "project")
	add.MarkFlagsMutuallyExclusive("body", "stdin")
	add.RunE = func(cmd *cobra.Command, args []string) error {
		if err := a.ready(); err != nil {
			return err
		}
		if projectOnly && a.project == "" {
			return errors.New("project prompts require a Git repository with a portable remote URL")
		}
		if title == "" && !stdin && !cmd.Flags().Changed("body") {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("use --title and --stdin or --body outside a terminal")
			}
			m := newModel(a)
			m.beginAdd()
			m.projectScope = projectOnly
			_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(a.out)).Run()
			return err
		}
		if stdin {
			b, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			body = string(b)
		}
		scope := ""
		if projectOnly {
			scope = a.project
		}
		p, err := store.New(title, body, scope)
		if err != nil {
			return err
		}
		if err := a.repo.Locked(func() error {
			if err := a.store.Save(p); err != nil {
				return err
			}
			a.warn(a.repo.Sync())
			return nil
		}); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Saved", p.ID)
		return nil
	}
	root.AddCommand(add)
	for _, name := range []string{"show", "copy"} {
		name := name
		cmd := &cobra.Command{Use: name + " ID", Short: map[string]string{"show": "Print a prompt body", "copy": "Copy a prompt and update its last usage"}[name], Args: cobra.ExactArgs(1)}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if err := a.ready(); err != nil {
				return err
			}
			a.sync()
			p, err := a.store.Find(args[0])
			if err != nil {
				return err
			}
			if name == "show" {
				fmt.Fprint(a.out, p.Body)
				return nil
			}
			clipErr := clipboard.Write(p.Body)
			if clipErr != nil {
				fmt.Fprintln(a.errOut, "Clipboard unavailable. Copy this prompt manually:")
				fmt.Fprintln(a.out, p.Body)
			} else {
				fmt.Fprintln(a.out, "Copied:", p.Title)
			}
			return a.touch(p.ID)
		}
		root.AddCommand(cmd)
	}
	sync := &cobra.Command{Use: "sync", Short: "Commit, pull, resolve conflicts and push", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.ready(); err != nil {
			return err
		}
		return a.repo.Locked(a.repo.Sync)
	}}
	root.AddCommand(sync)
	remoteCmd := &cobra.Command{Use: "remote", Short: "Show or configure the library remote", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.ready(); err != nil {
			return err
		}
		r := a.repo.Remote()
		if r == "" {
			r = "Local only"
		}
		fmt.Fprintln(a.out, r)
		return nil
	}}
	remoteCmd.AddCommand(&cobra.Command{Use: "set URL", Short: "Add or replace the remote and synchronize", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.ready(); err != nil {
			return err
		}
		return a.repo.Locked(func() error {
			if err := a.repo.SetRemote(args[0]); err != nil {
				return err
			}
			a.warn(a.repo.Sync())
			fmt.Fprintln(a.out, "Remote saved")
			return nil
		})
	}})
	remoteCmd.AddCommand(&cobra.Command{Use: "remove", Short: "Keep local history and stop remote synchronization", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.ready(); err != nil {
			return err
		}
		return a.repo.Locked(a.repo.RemoveRemote)
	}})
	root.AddCommand(remoteCmd)
	root.AddCommand(newSkillsCommand())
	return root
}

func (a app) ready() error {
	if !a.repo.Configured() {
		return errors.New("first run: use plib for setup, or plib init --local / --remote URL")
	}
	return nil
}
func (a app) warn(err error) {
	if err != nil {
		fmt.Fprintln(a.errOut, "Sync pending; local data retained:", err)
	}
}
func (a app) sync() { a.warn(a.repo.Locked(a.repo.Sync)) }
func (a app) touch(id string) error {
	return a.repo.Locked(func() error {
		p, err := a.store.Find(id)
		if err != nil {
			return err
		}
		p.LastUsed = time.Now().UTC()
		if err := a.store.Save(p); err != nil {
			return err
		}
		a.warn(a.repo.Sync())
		return nil
	})
}
func scopeLabel(p store.Prompt) string {
	if p.Project == "" {
		return "global"
	}
	return p.Project
}
func preview(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
