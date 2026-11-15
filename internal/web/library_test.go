package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/page"
)

func TestLibraryListsTheSaveFolders(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	resp := s.get("/library")
	if resp.code != http.StatusOK || strings.TrimSpace(resp.body) != `{"folders":["Work"],"library":"~/kapi/library"}` {
		t.Errorf("GET /library: %d %s", resp.code, resp.body)
	}
}

func TestSaveAnswersTheNewIDAndRefusesABadFolder(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	code, body := s.post("/save", `{"folder":"Work"}`)
	if code != http.StatusOK || body["id"] != s.id || body["path"] != s.path {
		t.Errorf("save: %d %v", code, body)
	}
	code, body = s.post("/save", `{"folder":"../out"}`)
	if msg, _ := body["error"].(string); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "bad folder name") {
		t.Errorf("save to ../out: %d %v, want 422", code, body)
	}
	if code, body := s.post("/unsave", `{}`); code != http.StatusOK || body["path"] != s.path {
		t.Errorf("unsave: %d %v", code, body)
	}
}

func TestAMovedPageRedirectsToItsNewIDKeepingThePathAndQuery(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	to := "/elsewhere/plan.html"
	s.pages.mu.Lock()
	s.pages.moved = map[string]string{"0123456789abcdef": to}
	s.pages.mu.Unlock()
	s.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp := s.get("/p/0123456789abcdef/export/markdown?block=aaaaaaaa")
	if want := "/" + token + "/p/" + page.ID(to) + "/export/markdown?block=aaaaaaaa"; resp.code != http.StatusTemporaryRedirect || resp.header.Get("Location") != want {
		t.Errorf("moved page: %d to %q, want 307 to %q", resp.code, resp.header.Get("Location"), want)
	}
}
