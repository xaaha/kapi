package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/page"
)

func TestIndexTitlesPagesByHeadingAndMarksMovedFiles(t *testing.T) {
	dir := t.TempDir()
	titled := filepath.Join(dir, "rollout.html")
	plain := filepath.Join(dir, "notes.html")
	gone := filepath.Join(dir, "gone.html")
	for path, src := range map[string]string{titled: "<h1>Rollout plan</h1><p>Two steps.</p>", plain: "<p>no heading</p>", gone: "<h1>Gone</h1>"} {
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, s := homeWith(t, "ki", []pageCount{{titled, 0}, {plain, 0}, {gone, 0}})
	if err := s.addOpener(titled, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	pages, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]int{}
	for i, p := range pages {
		byPath[p.Path] = i
		if p.ID != page.ID(p.Path) {
			t.Errorf("%s: id %q, want %q", p.Path, p.ID, page.ID(p.Path))
		}
	}
	if len(pages) != 3 {
		t.Fatalf("pages = %+v", pages)
	}
	if p := pages[byPath[titled]]; p.Title != "Rollout plan" || p.Summary != "Two steps." || len(p.Folders) != 1 || p.Folders[0] != resolved(dir) || p.Missing || p.Changed.IsZero() {
		t.Errorf("titled page = %+v", p)
	}
	if p := pages[byPath[plain]]; p.Title != "notes" || p.Missing {
		t.Errorf("plain page = %+v", p)
	}
	if p := pages[byPath[gone]]; !p.Missing || p.Title != "gone" {
		t.Errorf("moved page = %+v", p)
	}
}

func TestIndexRereadsAPageOnlyWhenItsFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.html")
	if err := os.WriteFile(path, []byte("<h1>First</h1><p>one</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, s := homeWith(t, "kc", []pageCount{{path, 0}})
	title := func() string {
		t.Helper()
		pages, err := s.Index()
		if err != nil || len(pages) != 1 {
			t.Fatalf("Index = %+v, %v", pages, err)
		}
		return pages[0].Title + " / " + pages[0].Summary
	}
	if got := title(); got != "First / one" {
		t.Fatalf("title = %q", got)
	}
	if err := os.WriteFile(path, []byte("<h1>Second title</h1><p>two</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := title(); got != "Second title / two" {
		t.Errorf("after an edit, title = %q", got)
	}
}
