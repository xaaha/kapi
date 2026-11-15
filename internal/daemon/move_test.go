package daemon

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
)

var errInjected = errors.New("injected failure")

func hook[F any](t *testing.T, target *F, fn F) {
	t.Helper()
	prev := *target
	*target = fn
	t.Cleanup(func() { *target = prev })
}

func assertDraftWhole(t *testing.T, s *Server, from string, img []byte) {
	t.Helper()
	src, err := os.ReadFile(from)
	if err != nil || !strings.Contains(string(src), `src="./plan.files/a.png"`) {
		t.Errorf("draft after the failed save = %q, %v", src, err)
	}
	if b, err := os.ReadFile(filepath.Join(page.FilesDir(from), "a.png")); err != nil || !bytes.Equal(b, img) {
		t.Errorf("draft image after the failed save = %q, %v", b, err)
	}
	if present(filepath.Join(s.pageState(from), intentName)) {
		t.Errorf("%s left behind after the undo", intentName)
	}
}

func assertNothingInLibrary(t *testing.T, lib string) {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(lib, "Work"))
	if len(ents) != 0 {
		names := []string{}
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("library folder after the failed save holds %v, want nothing", names)
	}
}

func TestASaveThatFailsAtAnyStepPutsEverythingBack(t *testing.T) {
	tests := []struct {
		name string
		hook func(t *testing.T, from string)
		want error
	}{
		{"placing the file", func(t *testing.T, _ string) {
			hook(t, &linkFile, func(string, string) error { return errInjected })
		}, errInjected},
		{"moving and copying the images", func(t *testing.T, _ string) {
			hook(t, &renamePath, func(string, string) error { return errInjected })
			hook(t, &linkFile, func(a, b string) error {
				if strings.HasSuffix(filepath.Dir(b), ".files") {
					return errInjected
				}
				return os.Link(a, b)
			})
		}, errInjected},
		{"removing the draft", func(t *testing.T, from string) {
			hook(t, &removePath, func(p string) error {
				if p == from {
					return errInjected
				}
				return os.Remove(p)
			})
		}, errInjected},
		{"removing the draft after an image copy", func(t *testing.T, from string) {
			hook(t, &renamePath, func(a, b string) error {
				if strings.HasSuffix(a, ".files") {
					return errInjected
				}
				return os.Rename(a, b)
			})
			hook(t, &removePath, func(p string) error {
				if p == from {
					return errInjected
				}
				return os.Remove(p)
			})
		}, errInjected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, s, _ := serving(t)
			lib := withLibrary(t, s)
			from, img := draftWithImage(t)
			call(t, home, &Request{Op: "open", Page: from})
			call(t, home, &Request{Op: "append", Page: from, Author: "human", Kind: "message", Text: "before"})
			tt.hook(t, from)
			if _, err := s.Save(from, "Work"); !errors.Is(err, tt.want) {
				t.Fatalf("Save = %v, want %v", err, tt.want)
			}
			assertDraftWhole(t, s, from, img)
			assertNothingInLibrary(t, lib)
			if r := call(t, home, &Request{Op: "append", Page: from, Author: "human", Kind: "message", Text: "after"}); r.Error != "" {
				t.Errorf("the draft no longer takes messages: %s", r.Error)
			}
			linkFile, renamePath, removePath = os.Link, os.Rename, os.Remove
			to, err := s.Save(from, "Work")
			if err != nil || to != filepath.Join(lib, "Work", "plan.html") {
				t.Errorf("retry = %q, %v; want the plain name, nothing stolen", to, err)
			}
		})
	}
}

func TestASaveAbortsWhenTheDraftIsReplacedMidMove(t *testing.T) {
	home, s, _ := serving(t)
	lib := withLibrary(t, s)
	from, _ := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	agent := []byte("<h1>Plan</h1>\n<p>the agent's rewrite</p>\n")
	hook(t, &linkFile, func(a, b string) error {
		if err := os.Link(a, b); err != nil || a != from {
			return err
		}
		tmp := from + ".agent"
		if err := os.WriteFile(tmp, agent, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, from)
	})
	if _, err := s.Save(from, "Work"); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("Save = %v, want ErrSourceChanged", err)
	}
	if b, _ := os.ReadFile(from); !bytes.Equal(b, agent) {
		t.Errorf("draft = %q, want the agent's write kept", b)
	}
	if !present(filepath.Join(page.FilesDir(from), "a.png")) {
		t.Error("the images did not come back")
	}
	assertNothingInLibrary(t, lib)
}

func TestAnImagesFolderLeftByACopyIsRemovedOnTheNextStart(t *testing.T) {
	home := shortHome(t)
	s, stop := servingAt(t, home)
	lib := withLibrary(t, s)
	from, img := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	hook(t, &renamePath, func(a, b string) error {
		if strings.HasSuffix(a, ".files") {
			return errInjected
		}
		return os.Rename(a, b)
	})
	hook(t, &removeTree, func(p string) error {
		if p == page.FilesDir(from) {
			if err := os.Remove(filepath.Join(p, "a.png")); err != nil {
				return err
			}
			return errInjected
		}
		return os.RemoveAll(p)
	})
	to, err := s.Save(from, "Work")
	if err != nil {
		t.Fatalf("Save = %v; a leftover source folder must not fail the save", err)
	}
	if b, err := os.ReadFile(filepath.Join(page.FilesDir(to), "a.png")); err != nil || !bytes.Equal(b, img) {
		t.Errorf("library image = %q, %v", b, err)
	}
	if r := call(t, home, &Request{Op: "open", Page: to}); r.Error != "" {
		t.Errorf("open after the save: %s", r.Error)
	}
	stop()
	removeTree = os.RemoveAll
	s2, _ := servingAt(t, home)
	if present(page.FilesDir(from)) {
		t.Error("the leftover images folder is still next to the old draft")
	}
	if present(filepath.Join(s2.pageState(to), intentName)) {
		t.Errorf("%s left behind", intentName)
	}
	if !present(filepath.Join(lib, "Work", "plan.files", "a.png")) {
		t.Error("cleanup touched the library copy")
	}
}

func TestAHistoryMoveThatFailsIsFinishedBeforeEitherPathOpens(t *testing.T) {
	home, s, _ := serving(t)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	call(t, home, &Request{Op: "append", Page: from, Author: "human", Kind: "message", Text: "keep this"})
	hook(t, &renameState, func(string, string) error { return errInjected })
	to, err := s.Save(from, "Work")
	if !errors.Is(err, errInjected) || to == "" {
		t.Fatalf("Save = %q, %v; want the new path and the history error", to, err)
	}
	for _, path := range []string{to, from} {
		if r := call(t, home, &Request{Op: "open", Page: path}); !strings.Contains(r.Error, "still being saved") {
			t.Errorf("open %s while the history is stuck = %+v, want a refusal", path, r)
		}
	}
	if present(filepath.Join(s.pageState(to), "events.ndjson")) {
		t.Error("a refused open created a fresh history at the new path")
	}
	renameState = os.Rename
	if r := call(t, home, &Request{Op: "open", Page: to}); r.Error != "" {
		t.Fatalf("open once the history can move: %s", r.Error)
	}
	l, err := s.page(to)
	if err != nil {
		t.Fatal(err)
	}
	if events, _ := l.After(0); !slices.ContainsFunc(events, func(e eventlog.Event) bool { return e.Text == "keep this" }) {
		t.Errorf("events at the new path = %+v, want the history", events)
	}
	if r := call(t, home, &Request{Op: "pending", Page: from, Reader: "agent"}); !strings.Contains(r.Error, "moved to "+to) {
		t.Errorf("old path = %+v, want the moved error", r)
	}
}

func TestAHistoryMoveThatFailedIsFinishedOnTheNextStart(t *testing.T) {
	home := shortHome(t)
	s, stop := servingAt(t, home)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	call(t, home, &Request{Op: "append", Page: from, Author: "human", Kind: "message", Text: "keep this"})
	hook(t, &renameState, func(string, string) error { return errInjected })
	to, err := s.Save(from, "Work")
	if !errors.Is(err, errInjected) {
		t.Fatalf("Save = %v", err)
	}
	stop()
	renameState = os.Rename
	s2, _ := servingAt(t, home)
	l, err := s2.page(to)
	if err != nil {
		t.Fatal(err)
	}
	if events, _ := l.After(0); !slices.ContainsFunc(events, func(e eventlog.Event) bool { return e.Text == "keep this" }) {
		t.Errorf("events at the new path after restart = %+v, want the history", events)
	}
}

type crashedSave struct {
	home, from, to string
	img            []byte
}

func crashSave(t *testing.T, collide bool, steps ...string) crashedSave {
	t.Helper()
	home := shortHome(t)
	from, img := draftWithImage(t)
	work := filepath.Join(t.TempDir(), "Work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(work, "plan.html")
	if collide {
		if err := os.WriteFile(to, []byte("theirs"), 0o600); err != nil {
			t.Fatal(err)
		}
		to = filepath.Join(work, "plan 2.html")
	}
	s := New(home)
	l, err := s.page(from)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "kept"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	j, err := s.beginMove(from, to, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		switch step {
		case stepPlaced:
			err = placeFile(j)
		case stepImages:
			err = moveImages(j)
		case "unlinked":
			err = os.Remove(from)
		case stepRemoved:
			err = errors.Join(os.Remove(from), j.advance(stepRemoved))
		}
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	return crashedSave{home, from, to, img}
}

func TestACrashAfterPlacingIsUndoneOnTheNextStart(t *testing.T) {
	c := crashSave(t, false, stepPlaced)
	s, _ := servingAt(t, c.home)
	if present(c.to) {
		t.Error("the placed copy is still in the library")
	}
	assertDraftWhole(t, s, c.from, c.img)
}

func TestACrashedCollisionSaveIsUndoneWithoutLeavingADuplicate(t *testing.T) {
	c := crashSave(t, true, stepPlaced, stepImages)
	s, _ := servingAt(t, c.home)
	if present(c.to) || present(page.FilesDir(c.to)) {
		t.Errorf("a duplicate with dead image links is left: %v %v", present(c.to), present(page.FilesDir(c.to)))
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(c.to), "plan.html")); string(b) != "theirs" {
		t.Errorf("the existing plan.html = %q", b)
	}
	assertDraftWhole(t, s, c.from, c.img)
}

func TestACrashAfterRemovingTheDraftFinishesAndLeavesANewDraftAlone(t *testing.T) {
	c := crashSave(t, false, stepPlaced, stepImages, stepRemoved)
	agent := []byte("<p>a new draft the agent wrote</p>")
	if err := os.WriteFile(c.from, agent, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := servingAt(t, c.home)
	if b, _ := os.ReadFile(c.from); !bytes.Equal(b, agent) {
		t.Errorf("new draft = %q, want it untouched", b)
	}
	if present(page.FilesDir(c.from)) {
		t.Error("the library's images came back next to the new draft")
	}
	if b, err := os.ReadFile(filepath.Join(page.FilesDir(c.to), "a.png")); err != nil || !bytes.Equal(b, c.img) {
		t.Errorf("library image = %q, %v", b, err)
	}
	l, err := s.page(c.to)
	if err != nil {
		t.Fatal(err)
	}
	if events, _ := l.After(0); len(events) == 0 || events[0].Text != "kept" {
		t.Errorf("history at the new path = %+v", events)
	}
	if lc, _ := page.ReadLifecycle(s.pageState(c.to)); !lc.Saved {
		t.Error("not marked saved")
	}
}

func TestACrashBetweenRemovingAndRecordingItFinishesTheSave(t *testing.T) {
	c := crashSave(t, false, stepPlaced, stepImages, "unlinked")
	s, _ := servingAt(t, c.home)
	if !present(c.to) || !present(filepath.Join(page.FilesDir(c.to), "a.png")) {
		t.Error("the save was not finished")
	}
	if b, _ := os.ReadFile(filepath.Join(s.pageState(c.to), "page.txt")); string(b) != c.to+"\n" {
		t.Errorf("page.txt = %q", b)
	}
	if to, moved := s.movedTo(c.from); !moved || to != c.to {
		t.Errorf("movedTo = %q %v", to, moved)
	}
	if _, err := os.Stat(c.from); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("draft = %v", err)
	}
}
