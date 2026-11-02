package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/illegalstudio/prompt-library/internal/project"
)

type Prompt struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	Project  string    `json:"project,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	LastUsed time.Time `json:"last_used"`
}

type Store struct{ Root string }

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func DefaultRoot() (string, error) {
	if root := os.Getenv("PLIB_HOME"); root != "" {
		return filepath.Abs(root)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".plib"), nil
}

func New(title, body, scope string) (Prompt, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Prompt{}, err
	}
	now := time.Now().UTC()
	p := Prompt{ID: hex.EncodeToString(id), Title: strings.TrimSpace(title), Body: body, Project: scope, Created: now, Updated: now}
	return p, Validate(p)
}

func Validate(p Prompt) error {
	if !validID.MatchString(p.ID) {
		return errors.New("invalid prompt ID")
	}
	if strings.TrimSpace(p.Title) == "" {
		return errors.New("title is required")
	}
	if strings.TrimSpace(p.Body) == "" {
		return errors.New("body is required")
	}
	return nil
}

func (s Store) Path(p Prompt) string {
	if p.Project == "" {
		return filepath.Join(s.Root, "global", p.ID+".json")
	}
	return filepath.Join(s.Root, "projects", project.Folder(p.Project), p.ID+".json")
}

func (s Store) Save(p Prompt) error {
	if err := Validate(p); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(s.Root, s.Path(p), append(b, '\n'))
}

// Delete removes one prompt. The caller must hold the repository lock.
func (s Store) Delete(p Prompt) error {
	if err := Validate(p); err != nil {
		return err
	}
	return os.Remove(s.Path(p))
}

// Replace preserves a prompt's identity when moving it between scope folders.
// The caller must hold the repository lock.
func (s Store) Replace(original, updated Prompt) error {
	if err := Validate(original); err != nil {
		return err
	}
	if original.ID != updated.ID {
		return errors.New("cannot change a prompt's ID")
	}
	oldPath, newPath := s.Path(original), s.Path(updated)
	if oldPath == newPath {
		return s.Save(updated)
	}
	if _, err := os.Lstat(newPath); err == nil {
		return errors.New("a prompt already exists in the destination scope")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.Save(updated); err != nil {
		return err
	}
	if err := os.Remove(oldPath); err != nil {
		// Keep the original record if a scope move cannot finish.
		return fmt.Errorf("move prompt: %w", errors.Join(err, os.Remove(newPath)))
	}
	return nil
}

func AtomicWrite(root, path string, b []byte) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("write path is outside the library")
	}
	// Refuse directory symlinks inside the library, while allowing system
	// ancestors such as macOS /var, which points to /private/var.
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		st, err := os.Lstat(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink directory %s", dir)
		}
		if dir == root {
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".plib-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

// List skips damaged files so a single bad record does not hide the library.
func (s Store) List(scope, query string, all bool) ([]Prompt, []string, error) {
	prompts := []Prompt{}
	warnings := []string{}
	for _, folder := range []string{"global", "projects"} {
		err := filepath.WalkDir(filepath.Join(s.Root, folder), func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".json" {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				warnings = append(warnings, "Skipped symlink: "+path)
				return nil
			}
			b, e := os.ReadFile(path)
			var p Prompt
			if e == nil {
				e = json.Unmarshal(b, &p)
			}
			if e == nil {
				e = Validate(p)
			}
			if e == nil && filepath.Clean(s.Path(p)) != filepath.Clean(path) {
				e = errors.New("prompt path does not match its identity")
			}
			if e != nil {
				warnings = append(warnings, "Skipped invalid prompt: "+path)
				return nil
			}
			if !all && p.Project != "" && p.Project != scope {
				return nil
			}
			haystack := strings.ToLower(p.Title + "\n" + p.Body)
			for _, word := range strings.Fields(strings.ToLower(query)) {
				if !strings.Contains(haystack, word) {
					return nil
				}
			}
			prompts = append(prompts, p)
			return nil
		})
		if err != nil {
			return nil, warnings, err
		}
	}
	sort.Slice(prompts, func(i, j int) bool {
		if !prompts[i].LastUsed.Equal(prompts[j].LastUsed) {
			return prompts[i].LastUsed.After(prompts[j].LastUsed)
		}
		if !prompts[i].Updated.Equal(prompts[j].Updated) {
			return prompts[i].Updated.After(prompts[j].Updated)
		}
		return prompts[i].ID < prompts[j].ID
	})
	return prompts, warnings, nil
}

func (s Store) Find(id string) (Prompt, error) {
	ps, _, err := s.List("", "", true)
	if err != nil {
		return Prompt{}, err
	}
	var matches []Prompt
	for _, p := range ps {
		if strings.HasPrefix(p.ID, id) {
			matches = append(matches, p)
		}
	}
	if len(matches) != 1 {
		return Prompt{}, fmt.Errorf("ID %q matches %d prompts; use a unique ID", id, len(matches))
	}
	return matches[0], nil
}
