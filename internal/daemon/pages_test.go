package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/xaaha/kapi/internal/eventlog"
)

type pageCount struct {
	path string
	n    int
}

func homeWith(t *testing.T, name string, pages []pageCount) (string, *Server) {
	t.Helper()
	home, err := os.MkdirTemp("", name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	s := New(home)
	for _, pc := range pages {
		page, n := pc.path, pc.n
		l, err := s.page(page)
		if err != nil {
			t.Fatal(err)
		}
		for range n {
			if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "hi"}); err != nil {
				t.Fatal(err)
			}
		}
		for _, r := range []string{"codex", "beta", "delta", "eve", "fay"} {
			if err := l.Ack(r, 1); err != nil && n > 0 {
				t.Fatal(err)
			}
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return home, New(home)
}

func TestPagesAreSortedReadOnlyAndKeepTheirExactPath(t *testing.T) {
	_, s := homeWith(t, "kd", []pageCount{{"/tmp/z.html", 1}, {"/tmp/a.html", 2}, {"/tmp/ trail.html ", 3}})
	got, err := s.listPages()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/tmp/ trail.html ", "/tmp/a.html", "/tmp/z.html"}
	if len(got) != 3 || got[0].Path != want[0] || got[1].Path != want[1] || got[2].Path != want[2] {
		t.Fatalf("pages = %+v", got)
	}
	r := got[0].Readers
	names := make([]string, len(r))
	for i, x := range r {
		names[i] = x.Reader
	}
	if !slices.Equal(names, []string{"agent", "beta", "codex", "delta", "eve", "fay"}) || r[0].Pending != 3 || r[1].Pending != 2 {
		t.Errorf("readers = %+v", r)
	}
	if len(s.pages) != 0 {
		t.Errorf("listing opened %d logs for writing", len(s.pages))
	}
}

func TestOneBrokenPageDoesNotHideTheOthers(t *testing.T) {
	home, s := homeWith(t, "kd[3]", []pageCount{{"/tmp/good.html", 1}, {"/tmp/bad.html", 1}})
	dirs, err := os.ReadDir(filepath.Join(home, "pages"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		b, _ := os.ReadFile(filepath.Join(home, "pages", d.Name(), "page.txt"))
		if string(b) == "/tmp/bad.html\n" {
			if err := os.WriteFile(filepath.Join(home, "pages", d.Name(), "events.ndjson"), []byte("not json\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := s.listPages()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "/tmp/bad.html" || got[0].Error == "" || got[1].Path != "/tmp/good.html" || got[1].Readers[0].Pending != 1 {
		t.Errorf("pages = %+v", got)
	}
}
