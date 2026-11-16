package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/page"
)

func TestMissingPageRecoversItsNewestVersion(t *testing.T) {
	_, s, _ := serving(t)
	dir := filepath.Join(t.TempDir(), "scratchpad")
	path, id := openWithImage(t, s, dir)
	want := mustRead(t, path)
	if _, ok := s.Missing(id); ok {
		t.Fatal("a page whose file exists reports missing")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Missing(id)
	if !ok || m.Rev != 1 || m.At.IsZero() || m.Title != "Rollout plan" || m.Path != path {
		t.Fatalf("missing = %+v, %t; want rev 1 titled from the copy", m, ok)
	}
	got, err := s.Recover(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rev != 1 {
		t.Errorf("recovered rev %d, want 1", got.Rev)
	}
	if mustRead(t, path) != want {
		t.Errorf("recovered file = %q, want %q", mustRead(t, path), want)
	}
	if _, ok := s.Missing(id); ok {
		t.Error("still missing after recover")
	}
	if _, err := s.Recover(id); !errors.Is(err, fs.ErrExist) {
		t.Errorf("second recover: %v, want fs.ErrExist", err)
	}
	if _, l, err := s.Page(id); err != nil || l == nil {
		t.Errorf("page after recover: %v", err)
	}
}

func TestMissingIgnoresMovedAndDeletedPages(t *testing.T) {
	_, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Missing(id); ok {
		t.Error("a deleted page reports missing")
	}
	if _, err := s.Recover(id); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("recover a deleted page: %v, want fs.ErrNotExist", err)
	}
}

func TestRecoverOpRestoresForTheCLI(t *testing.T) {
	_, s, _ := serving(t)
	path, _ := openWithImage(t, s, t.TempDir())
	if resp := s.recoverOp(path); resp.Notice != "" || resp.Error != "" {
		t.Fatalf("recover op on a present file = %+v, want nothing", resp)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	resp := s.recoverOp(path)
	if resp.Error != "" || !strings.Contains(resp.Notice, "restored version 1") {
		t.Fatalf("recover op = %+v, want a notice naming version 1", resp)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(t.TempDir(), "never.html")
	if resp := s.recoverOp(unknown); resp.Notice != "" || resp.Error != "" {
		t.Errorf("recover op on a page kapi never saw = %+v, want nothing", resp)
	}
	if _, err := os.Stat(filepath.Join(s.home, "pages", page.ID(unknown))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("recover op made state for an unknown page: %v", err)
	}
}
