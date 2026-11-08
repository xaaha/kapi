package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/web/app"
)

func TestHomeAndPagesLoadEveryServiceFromItsOwnFolder(t *testing.T) {
	s := newSiteNamed(t, "rollout.html", `<h1 data-kapi-id="aaaaaaaa">Rollout plan</h1>`)
	home := s.get("/")
	if home.code != http.StatusOK || !strings.Contains(home.body, `<main id="kapi-home"></main>`) {
		t.Fatalf("GET /: %d\n%s", home.code, home.body)
	}
	pageHTML := s.get("/p/" + s.id + "/").body
	for _, svc := range services(s.pages) {
		for _, tc := range []struct{ body, root string }{{home.body, ""}, {pageHTML, "../../"}} {
			for _, want := range []string{
				`<script type="module" src="` + tc.root + `assets/` + svc.Name() + `/main.js"></script>`,
				`<link rel="stylesheet" href="` + tc.root + `assets/` + svc.Name() + `/style.css">`,
			} {
				if !strings.Contains(tc.body, want) {
					t.Errorf("%s shell lacks %q", svc.Name(), want)
				}
			}
		}
		for _, f := range []string{"main.js", "style.css"} {
			if resp := s.get("/assets/" + svc.Name() + "/" + f); resp.code != http.StatusOK || resp.header.Get("Cache-Control") != "no-cache" {
				t.Errorf("GET %s/%s: %d %q", svc.Name(), f, resp.code, resp.header.Get("Cache-Control"))
			}
		}
	}
	for _, p := range []string{"/static/", "/static/fonts", "/static/fonts/", "/static/nope.js", "/assets/notebook/", "/assets/nope/main.js"} {
		if resp := s.get(p); resp.code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", p, resp.code)
		}
	}
}

func TestNotebookListsPagesWithTheirTitles(t *testing.T) {
	s := newSiteNamed(t, "rollout.html", `<h1 data-kapi-id="aaaaaaaa">Rollout plan</h1>`)
	resp := s.get("/api/notebook/pages")
	var pages []app.Page
	if err := json.Unmarshal([]byte(resp.body), &pages); err != nil || resp.code != http.StatusOK {
		t.Fatalf("GET pages: %d %v\n%s", resp.code, err, resp.body)
	}
	if len(pages) != 1 || pages[0].ID != s.id || pages[0].Path != s.path || pages[0].Title != "rollout" {
		t.Errorf("pages = %+v", pages)
	}
}

func TestTheTokenRootWithoutASlashRedirectsToTheHome(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp := s.raw("GET", "/"+token, nil)
	if resp.code != http.StatusPermanentRedirect || resp.header.Get("Location") != "/"+token+"/" {
		t.Errorf("GET /<token>: %d to %q, want 308 to /<token>/", resp.code, resp.header.Get("Location"))
	}
	if home := s.get("/"); home.code != http.StatusOK || !strings.Contains(home.body, `src="assets/notebook/main.js"`) {
		t.Errorf("GET /<token>/: %d", home.code)
	}
	if resp := s.raw("GET", "/"+token+"x", nil); resp.code != http.StatusForbidden {
		t.Errorf("GET /<wrong token>: %d, want 403", resp.code)
	}
}

func TestTheNotebookStreamCountsATabAndSaysWhenPagesChange(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.srv.URL+"/"+token+"/api/notebook/events", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
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
	if n := s.pages.tabCount(); n != 1 {
		t.Errorf("tabs = %d, want 1 while the stream is open", n)
	}
	s.pages.change()
	next()
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for s.pages.tabCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the tab is still counted after the stream closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
