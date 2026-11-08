package daemon

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenShowsThePageInAnOpenNotebookTabInsteadOfANewOne(t *testing.T) {
	home, s, _ := serving(t)
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.html"), filepath.Join(dir, "second.html")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("<h1>x</h1>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := call(t, home, &Request{Op: "open", Page: first, Dir: dir}); got.Error != "" || got.Shown {
		t.Fatalf("open with no tab = %+v, want shown false", got)
	}

	base, err := os.ReadFile(HTTPURLPath(home))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(string(base))+"api/notebook/events", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	next := func() {
		t.Helper()
		for lines.Scan() {
			if lines.Text() == "data: changed" {
				return
			}
		}
		t.Fatalf("stream ended: %v", lines.Err())
	}
	next()

	got := call(t, home, &Request{Op: "open", Page: second, Dir: dir})
	if got.Error != "" || !got.Shown {
		t.Fatalf("open with a tab = %+v, want shown true", got)
	}
	next()
	pages, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if p.Opened.IsZero() || time.Since(p.Opened) > time.Minute {
			t.Errorf("%s opened at %v", p.Path, p.Opened)
		}
	}

	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := call(t, home, &Request{Op: "open", Page: first, Dir: dir}); !got.Shown {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("open still reports a tab after the stream closed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
