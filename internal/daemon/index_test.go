package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/web/app"
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

func TestIndexStateFollowsTheSessionForAnyAgentReader(t *testing.T) {
	dir := t.TempDir()
	quiet, asked, readByCodex, readByAgent, watched, ended := filepath.Join(dir, "quiet.html"), filepath.Join(dir, "asked.html"),
		filepath.Join(dir, "codex.html"), filepath.Join(dir, "agent.html"), filepath.Join(dir, "watched.html"), filepath.Join(dir, "ended.html")
	home, err := os.MkdirTemp("", "ks")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	first := New(home)
	for path, reader := range map[string]string{quiet: "", asked: "", readByCodex: "codex", readByAgent: "agent", watched: "", ended: ""} {
		l, err := first.page(path)
		if err != nil {
			t.Fatal(err)
		}
		if path != quiet {
			e, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "please review"})
			if err != nil {
				t.Fatal(err)
			}
			if reader != "" {
				if err := l.Ack(reader, e.Seq); err != nil {
					t.Fatal(err)
				}
			}
		}
		if path == ended {
			if _, err := l.End("human"); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{quiet: app.Idle, asked: app.Waiting, readByCodex: app.Idle, readByAgent: app.Idle, watched: app.Waiting, ended: app.Ended}
	check := func(s *Server, how string) {
		t.Helper()
		pages, err := s.Index()
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != len(want) {
			t.Fatalf("%s: pages = %+v", how, pages)
		}
		for _, p := range pages {
			if p.State != want[p.Path] {
				t.Errorf("%s: %s state %q, want %q", how, filepath.Base(p.Path), p.State, want[p.Path])
			}
		}
	}
	s := New(home)
	check(s, "closed logs")
	for path := range want {
		if _, err := s.page(path); err != nil {
			t.Fatal(err)
		}
	}
	check(s, "open logs")
	s.setAttached(watched, "codex", true)
	want[watched] = app.Live
	check(s, "a named reader watching")
}
