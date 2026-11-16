package page

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverWritesTheNewestCopyBack(t *testing.T) {
	home, path := newPage(t, "<p>one</p>\n")
	s := open(t, home, path)
	if err := os.WriteFile(path, []byte("<p>two</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	want, rev := s.Source()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	c, err := Recover(home, path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Rev != rev || c.At.IsZero() {
		t.Errorf("recovered rev %d at %v, want rev %d with a time", c.Rev, c.At, rev)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file = %q, %v; want %q", got, err, want)
	}
	if _, changed, err := s.Refresh(); err != nil || changed {
		t.Errorf("refresh after recover: changed %t, %v; want no new revision", changed, err)
	}
}

func TestRecoverRecreatesTheFolder(t *testing.T) {
	home, path := newPage(t, "<p>one</p>\n")
	dir := filepath.Join(filepath.Dir(path), "scratch")
	nested := filepath.Join(dir, "plan.html")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, nested); err != nil {
		t.Fatal(err)
	}
	open(t, home, nested)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(home, nested); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatal(err)
	}
	if temps, _ := findTemps(dir, ""); len(temps) != 0 {
		t.Errorf("left temp files: %v", temps)
	}
}

func TestRecoverNeverOverwrites(t *testing.T) {
	home, path := newPage(t, "<p>one</p>\n")
	open(t, home, path)
	if err := os.WriteFile(path, []byte("<p>agent wrote this</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(home, path); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("recover over a file: %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "<p>agent wrote this</p>\n" {
		t.Errorf("file = %q, want it untouched", got)
	}
}

func TestRecoverWithoutACopy(t *testing.T) {
	home, path := newPage(t, "<p>one</p>\n")
	if c, err := LatestCopy(home, path); err != nil || c.Rev != 0 {
		t.Fatalf("latest copy of an unseen page: %+v, %v", c, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(home, path); !errors.Is(err, ErrNoCopy) {
		t.Fatalf("recover: %v, want ErrNoCopy", err)
	}
}
