package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKapiHomeStartsTheDaemonAndOpensTheNotebook(t *testing.T) {
	e := newE2E(t)
	url := strings.TrimSpace(e.kapi("home"))
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") || strings.Contains(url, "/p/") {
		t.Fatalf("kapi home printed %q, want the notebook URL", url)
	}
	waitFor(t, "the browser to open the notebook", func() bool { b, _ := os.ReadFile(e.opened); return strings.TrimSpace(string(b)) == url })
	if got := strings.TrimSpace(e.kapi("home", "--print")); got != url {
		t.Errorf("kapi home --print = %q, want %q", got, url)
	}
	e.env = append(e.env, "KAPI_NO_BROWSER=1")
	e.kapi("home")
	if b, _ := os.ReadFile(e.opened); strings.Count(string(b), "\n") != 1 {
		t.Errorf("--print or KAPI_NO_BROWSER opened the browser again: %q", b)
	}
}

func TestKapiLsListsTheLastViewedPageFirstAcrossARestart(t *testing.T) {
	e := newE2E(t)
	e.env = append(e.env, "KAPI_NO_BROWSER=1")
	dir := filepath.Dir(e.page)
	urls := map[string]string{}
	for i, name := range []string{"first.html", "second.html"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("<h1>"+strings.TrimSuffix(name, ".html")+" page</h1>\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		written := time.Now().Add(time.Duration(i-2) * time.Hour)
		if err := os.Chtimes(path, written, written); err != nil {
			t.Fatal(err)
		}
		urls[name] = strings.TrimSpace(e.kapi("open", path))
	}
	order := func() []string {
		var pages []struct{ Title, Path, State, Viewed, Touched string }
		if err := json.Unmarshal([]byte(e.kapi("ls", "--json")), &pages); err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, p := range pages {
			titles = append(titles, p.Title)
		}
		return titles
	}
	if got := order(); strings.Join(got, ",") != "second page,first page" {
		t.Fatalf("before viewing, kapi ls = %v, want the newer page first", got)
	}
	e.view(urls["first.html"])()
	waitFor(t, "kapi ls to list the viewed page first", func() bool { return strings.Join(order(), ",") == "first page,second page" })
	e.killDaemon()
	if got := order(); strings.Join(got, ",") != "first page,second page" {
		t.Errorf("after a restart, kapi ls = %v, want the viewed page still first", got)
	}
	table := e.kapi("ls")
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "TITLE") || !strings.HasPrefix(lines[1], "first page") || !strings.Contains(lines[1], "just now") || !strings.HasSuffix(lines[1], filepath.Join(dir, "first.html")) {
		t.Errorf("kapi ls =\n%s", table)
	}
}
