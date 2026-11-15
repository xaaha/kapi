package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

func (s *site) notebook(method, path string) (int, map[string]string) {
	s.t.Helper()
	resp := s.raw(method, "/"+token+"/api/notebook"+path, http.Header{"Origin": {s.srv.URL}, "Content-Type": {"application/json"}})
	var out map[string]string
	if err := json.Unmarshal([]byte(resp.body), &out); err != nil {
		s.t.Fatalf("%s %s: %d, body is not JSON: %v\n%s", method, path, resp.code, err, resp.body)
	}
	return resp.code, out
}

func TestNotebookDeletesAPageAndBringsItBack(t *testing.T) {
	s := newSiteNamed(t, "rollout.html", `<h1 data-kapi-id="aaaaaaaa">Rollout plan</h1>`)
	code, out := s.notebook("POST", "/pages/"+s.id+"/delete")
	if code != http.StatusOK || out["id"] != s.id || out["title"] != "rollout" {
		t.Fatalf("delete: %d %v", code, out)
	}
	if code, out := s.notebook("POST", "/pages/"+s.id+"/delete"); code != http.StatusNotFound || out["error"] == "" {
		t.Fatalf("delete again: %d %v", code, out)
	}
	if code, out := s.notebook("POST", "/pages/"+s.id+"/restore"); code != http.StatusOK || out["id"] != s.id {
		t.Fatalf("restore: %d %v", code, out)
	}
	if code, _ := s.notebook("POST", "/pages/"+s.id+"/restore"); code != http.StatusNotFound {
		t.Fatalf("restore again: %d", code)
	}
}

func TestNotebookSaysWhyADeleteOrRestoreFailed(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s.pages.lifeError = errors.New("could not move plan.html to the Trash")
	if code, out := s.notebook("POST", "/pages/"+s.id+"/delete"); code != http.StatusInternalServerError || out["error"] != "could not move plan.html to the Trash" {
		t.Fatalf("delete: %d %v", code, out)
	}
	s.pages.lifeError = errors.New("could not put plan.html back")
	if code, out := s.notebook("POST", "/pages/"+s.id+"/restore"); code != http.StatusConflict || out["error"] != "could not put plan.html back" {
		t.Fatalf("restore: %d %v", code, out)
	}
}

func TestNotebookDeleteRefusesARequestFromAnotherSite(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	resp := s.raw("POST", "/"+token+"/api/notebook/pages/"+s.id+"/delete", http.Header{"Origin": {"http://evil.example"}, "Content-Type": {"application/json"}})
	if resp.code != http.StatusForbidden {
		t.Fatalf("cross-site delete: %d", resp.code)
	}
	if _, err := s.pages.PagePath(s.id); err != nil {
		t.Fatalf("the page went away: %v", err)
	}
}
