package page

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameImageRefsPointsOnlyThePagesOwnImagesAtTheNewFolder(t *testing.T) {
	src := `<img src="./plan.files/a.png"><p>./plan.files/ in text</p><img alt="x" src="./plan%2Efiles/b.png">` +
		`<img src="./other.files/c.png"><img src="https://example.com/plan.files/d.png"><a href="./plan.files/e.png">e</a>`
	got := string(RenameImageRefs([]byte(src), "/drafts/plan.html", "/lib/Work/plan 2.html"))
	want := `<img src="./plan%202.files/a.png"><p>./plan.files/ in text</p><img alt="x" src="./plan%202.files/b.png">` +
		`<img src="./other.files/c.png"><img src="https://example.com/plan.files/d.png"><a href="./plan.files/e.png">e</a>`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestNumbered(t *testing.T) {
	for _, tt := range []struct {
		name string
		n    int
		want string
	}{{"plan.html", 1, "plan.html"}, {"plan.html", 2, "plan 2.html"}, {"a.b.html", 3, "a.b 3.html"}, {"notes", 2, "notes 2"}} {
		if got := Numbered(tt.name, tt.n); got != tt.want {
			t.Errorf("Numbered(%q, %d) = %q, want %q", tt.name, tt.n, got, tt.want)
		}
	}
}

func TestSavedSurvivesEveryStateWriteAndUnsaveRestartsTheClock(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">x</p>`)
	s := open(t, home, path)
	dir := filepath.Join(home, "pages", ID(path))
	if err := s.SetSaved(true); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkViewed(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">y</p>`}); err != nil {
		t.Fatal(err)
	}
	if lc, err := ReadLifecycle(dir); !lc.Saved || err != nil {
		t.Fatalf("saved = %v, %v after a view and an edit", lc.Saved, err)
	}
	if !open(t, home, path).Saved() {
		t.Error("a reopened store forgot the flag")
	}
	before := s.Lifecycle().Touched
	clock = func() time.Time { return before.Add(time.Hour) }
	t.Cleanup(func() { clock = time.Now })
	if err := s.SetSaved(false); err != nil {
		t.Fatal(err)
	}
	if lc, _ := ReadLifecycle(dir); lc.Saved {
		t.Error("still saved")
	}
	if touched := s.Lifecycle().Touched; !touched.Equal(before.Add(time.Hour).UTC()) {
		t.Errorf("touched = %v, want the unsave time", touched)
	}
}

func TestARetiredStoreRefusesWrites(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">x</p>`)
	s := open(t, home, path)
	if err := s.Retire(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">y</p>`}); !errors.Is(err, ErrRetired) {
		t.Errorf("Apply = %v, want ErrRetired", err)
	}
	if err := s.MarkViewed(); !errors.Is(err, ErrRetired) {
		t.Errorf("MarkViewed = %v, want ErrRetired", err)
	}
	if _, _, err := s.Refresh(); !errors.Is(err, ErrRetired) {
		t.Errorf("Refresh = %v, want ErrRetired", err)
	}
}

func TestMovedErrorCountsAsNotFound(t *testing.T) {
	err := error(&MovedError{From: "/a.html", To: "/b.html"})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("a moved page does not count as missing")
	}
	if got := err.Error(); got != `/a.html moved to /b.html when it was saved; use the new path from now on, e.g. kapi wait "/b.html"` {
		t.Errorf("message = %q", got)
	}
}
