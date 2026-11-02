package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilterSearchOrderAndRoundTrip(t *testing.T) {
	s := Store{Root: t.TempDir()}
	global, _ := New("General review", "First line\nSecond line\n", "")
	current, _ := New("Deploy", "Inspect migrations", "github.com/acme/api")
	other, _ := New("Other", "Some content", "github.com/other/api")
	global.LastUsed = time.Now().UTC()
	current.LastUsed = global.LastUsed.Add(-time.Hour)
	for _, p := range []Prompt{global, current, other} {
		if err := s.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	ps, w, err := s.List(current.Project, "", false)
	if err != nil || len(w) != 0 || len(ps) != 2 || ps[0].ID != global.ID {
		t.Fatalf("list: %+v %v %v", ps, w, err)
	}
	if ps[0].Body != global.Body {
		t.Fatal("multiline body changed")
	}
	ps, _, _ = s.List(current.Project, "MIGRATIONS", false)
	if len(ps) != 1 || ps[0].ID != current.ID {
		t.Fatal("body search failed")
	}
	ps, _, _ = s.List("", "review second", false)
	if len(ps) != 1 {
		t.Fatal("combined title/body search failed")
	}
	ps, _, _ = s.List("", "", true)
	if len(ps) != 3 {
		t.Fatal("all-project filter failed")
	}
	if err := os.WriteFile(filepath.Join(s.Root, "global", "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	ps, w, err = s.List("", "", true)
	if err != nil || len(w) != 1 || len(ps) != 3 {
		t.Fatal("one invalid prompt hid the library")
	}
	p, err := s.Find(global.ID[:8])
	if err != nil || p.ID != global.ID {
		t.Fatal("prefix lookup failed")
	}
	if _, err := s.Find(""); err == nil {
		t.Fatal("ambiguous ID accepted")
	}
}
func TestRejectUnsafeAndEmptyPrompt(t *testing.T) {
	s := Store{Root: t.TempDir()}
	p, _ := New("Title", "Body", "")
	p.ID = "../escape"
	if err := s.Save(p); err == nil {
		t.Fatal("unsafe ID accepted")
	}
	if _, err := New(" ", "Body", ""); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := New("Title", "\n", ""); err == nil {
		t.Fatal("empty body accepted")
	}
}

func TestWriteAllowsSystemSymlinkAncestorsButRejectsLibraryEscapes(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	s := Store{Root: filepath.Join(alias, "library")}
	p, _ := New("Title", "Body", "")
	if err := s.Save(p); err != nil {
		t.Fatal("system symlink ancestor prevented a save:", err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	s2 := Store{Root: filepath.Join(base, "library2")}
	if err := os.Mkdir(s2.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s2.Root, "global")); err != nil {
		t.Fatal(err)
	}
	if err := s2.Save(p); err == nil {
		t.Fatal("library directory symlink escaped storage")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("data was written outside the library")
	}
}
