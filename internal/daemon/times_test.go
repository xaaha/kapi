package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/web/app"
)

func indexOf(t *testing.T, home string) (order []string, byPath map[string]app.Page) {
	t.Helper()
	resp := call(t, home, &Request{Op: "index"})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	byPath = map[string]app.Page{}
	for i := range resp.Index {
		name := filepath.Base(resp.Index[i].Path)
		order = append(order, name)
		byPath[name] = resp.Index[i]
	}
	return order, byPath
}

func TestTheIndexListsMostRecentlyViewedFirstThenMostRecentlyTouchedAndKeepsBothAcrossARestart(t *testing.T) {
	home := shortHome(t)
	dir := t.TempDir()
	written := map[string]time.Time{}
	for i, name := range []string{"a.html", "b.html", "c.html", "d.html"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("<h1>"+name+"</h1>\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		written[name] = time.Date(2020, 1, i+1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(path, written[name], written[name]); err != nil {
			t.Fatal(err)
		}
	}
	_, stop := servingAt(t, home)
	urls := map[string]string{}
	for _, name := range []string{"a.html", "b.html", "c.html"} {
		urls[name] = call(t, home, &Request{Op: "open", Page: filepath.Join(dir, name)}).URL
	}
	call(t, home, &Request{Op: "append", Page: filepath.Join(dir, "d.html"), Author: "human", Kind: "message", Text: "hi"})

	if order, pages := indexOf(t, home); !slices.Equal(order, []string{"d.html", "c.html", "b.html", "a.html"}) {
		t.Fatalf("before any view, order = %v, want newest file first", order)
	} else if p := pages["d.html"]; !p.Touched.Equal(written["d.html"]) || !p.Viewed.Equal(p.Touched) {
		t.Errorf("a page kapi never stored times for: viewed %v touched %v, want both at its file time %v", p.Viewed, p.Touched, written["d.html"])
	}

	connectViewer(t, urls["a.html"]).close()
	waitUntil(t, "a.html to count as viewed", func() bool { order, _ := indexOf(t, home); return len(order) > 0 && order[0] == "a.html" })
	connectViewer(t, urls["b.html"]).close()
	waitUntil(t, "b.html to count as viewed", func() bool { order, _ := indexOf(t, home); return len(order) > 0 && order[0] == "b.html" })

	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(Request{Op: "wait", Page: filepath.Join(dir, "c.html"), Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	call(t, home, &Request{Op: "pending", Page: filepath.Join(dir, "c.html"), Reader: "agent"})
	conn.Close()

	want := []string{"b.html", "a.html", "d.html", "c.html"}
	order, before := indexOf(t, home)
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for _, name := range []string{"a.html", "b.html", "c.html"} {
		if p := before[name]; !p.Touched.Equal(written[name]) {
			t.Errorf("%s: touched %v, want it left at its file time %v by viewing and waiting", name, p.Touched, written[name])
		}
	}
	if !before["b.html"].Viewed.After(before["a.html"].Viewed) || !before["a.html"].Viewed.After(written["d.html"]) {
		t.Errorf("viewed times = a %v, b %v", before["a.html"].Viewed, before["b.html"].Viewed)
	}

	stop()
	servingAt(t, home)
	order, after := indexOf(t, home)
	if !slices.Equal(order, want) {
		t.Errorf("after a restart, order = %v, want %v", order, want)
	}
	for _, name := range want {
		if !after[name].Viewed.Equal(before[name].Viewed) || !after[name].Touched.Equal(before[name].Touched) {
			t.Errorf("%s after a restart: viewed %v touched %v, want %v and %v", name, after[name].Viewed, after[name].Touched, before[name].Viewed, before[name].Touched)
		}
	}
}

func TestHomeGivesTheNotebookURL(t *testing.T) {
	home := shortHome(t)
	servingAt(t, home)
	got := call(t, home, &Request{Op: "home"})
	if got.Error != "" || got.URL != httpURL(t, home) {
		t.Errorf("home = %+v, want %s", got, httpURL(t, home))
	}
}
