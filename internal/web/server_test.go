package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
)

const token = "s3cret"

type fakePages struct {
	mu    sync.Mutex
	home  string
	paths map[string]string
	logs  map[string]*eventlog.Log
	pages map[string]*page.Store
}

func (f *fakePages) Page(id string) (*page.Store, *eventlog.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, ok := f.paths[id]
	if !ok {
		return nil, nil, fs.ErrNotExist
	}
	if st, ok := f.pages[id]; ok {
		return st, f.logs[id], nil
	}
	st, err := page.Open(f.home, path)
	if err != nil {
		return nil, nil, err
	}
	l, err := eventlog.Open(filepath.Join(f.home, "pages", id))
	if err != nil {
		return nil, nil, err
	}
	f.pages[id], f.logs[id] = st, l
	return st, l, nil
}

type site struct {
	t      *testing.T
	srv    *httptest.Server
	web    *Server
	pages  *fakePages
	path   string
	id     string
	client *http.Client
}

func newSite(t *testing.T, src string) *site {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.html")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	id := page.ID(path)
	pages := &fakePages{home: filepath.Join(dir, "home"), paths: map[string]string{id: path}, logs: map[string]*eventlog.Log{}, pages: map[string]*page.Store{}}
	srv := httptest.NewUnstartedServer(nil)
	web := New(pages, token, srv.Listener.Addr().String())
	srv.Config.Handler = web
	srv.Start()
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &site{t: t, srv: srv, web: web, pages: pages, path: path, id: id, client: &http.Client{Jar: jar}}
	if resp := s.get("/p/" + id + "/?t=" + token); resp.code != http.StatusOK {
		t.Fatalf("first load with the token: %d", resp.code)
	}
	return s
}

type got struct {
	code   int
	header http.Header
	body   string
}

func (s *site) get(path string) got {
	s.t.Helper()
	resp, err := s.client.Get(s.srv.URL + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return got{resp.StatusCode, resp.Header, string(b)}
}

func (s *site) post(path, body string) (int, map[string]any) {
	s.t.Helper()
	resp, err := s.client.Post(s.srv.URL+"/p/"+s.id+path, "application/json", strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		s.t.Fatalf("POST %s: %s, body is not JSON: %v", path, resp.Status, err)
	}
	return resp.StatusCode, out
}

func (s *site) events() []eventlog.Event {
	s.t.Helper()
	_, l, err := s.pages.Page(s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	events, _ := l.After(0)
	return events
}

func TestTokenSetsAStrictHttpOnlyCookieAndLeavesTheURL(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(s.srv.URL + "/p/" + s.id + "/?t=" + token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/p/"+s.id+"/" {
		t.Fatalf("token load = %s to %q, want a 303 to the bare page URL", resp.Status, resp.Header.Get("Location"))
	}
	c := resp.Cookies()
	if len(c) != 1 || c[0].Value != token || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookies = %+v", c)
	}

	resp, err = noRedirect.Get(s.srv.URL + "//evil.example/?t=" + token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/evil.example/" {
		t.Errorf("redirect for //evil.example/ goes to %q, want a path on this server", loc)
	}
}

func TestRequestsWithoutTheTokenAreRefused(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, tc := range []struct {
		name string
		req  func() *http.Request
		want int
	}{
		{"no token", func() *http.Request { return httptest.NewRequest("GET", s.srv.URL+"/p/"+s.id+"/", nil) }, http.StatusUnauthorized},
		{"wrong query token", func() *http.Request { return httptest.NewRequest("GET", s.srv.URL+"/p/"+s.id+"/?t=nope", nil) }, http.StatusForbidden},
		{"wrong cookie", func() *http.Request {
			r := httptest.NewRequest("POST", s.srv.URL+"/p/"+s.id+"/messages", strings.NewReader(`{"kind":"message","text":"x"}`))
			r.AddCookie(&http.Cookie{Name: cookieName, Value: "nope"}) //nolint:gosec // a forged cookie
			return r
		}, http.StatusUnauthorized},
		{"static without token", func() *http.Request { return httptest.NewRequest("GET", s.srv.URL+"/static/kapi.js", nil) }, http.StatusUnauthorized},
	} {
		r := tc.req()
		r.RequestURI = ""
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %s, want %d", tc.name, resp.Status, tc.want)
		}
	}
	if n := len(s.events()); n != 0 {
		t.Errorf("%d events appended by refused requests", n)
	}
}

func TestAnotherHostIsRefusedEvenWithTheToken(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, host := range []string{"evil.example:80", "localhost:" + strconv.Itoa(s.srv.Listener.Addr().(*net.TCPAddr).Port)} {
		r, err := http.NewRequest("GET", s.srv.URL+"/p/"+s.id+"/?t="+token, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = host
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || len(resp.Cookies()) != 0 {
			t.Errorf("Host %s: %s with cookies %v, want 403 and no cookie", host, resp.Status, resp.Cookies())
		}
	}
}

func TestPageServesTheRenderedBodyWithRevisionAndAssets(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">hello &amp; welcome</p>`)
	page := s.get("/p/" + s.id + "/").body
	for _, want := range []string{`<main id="kapi-page" data-rev="1">`, `hello &amp; welcome`, `<link rel="stylesheet" href="/static/chrome.css">`, `<script src="/static/kapi.js" defer></script>`, `<title>plan.html</title>`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	for _, p := range []string{"/static/kapi.js", "/static/chrome.css"} {
		if resp := s.get(p); resp.code != http.StatusOK {
			t.Errorf("GET %s: %d", p, resp.code)
		}
	}
	if resp := s.get("/p/0123456789abcdef/"); resp.code != http.StatusNotFound {
		t.Errorf("unknown page: %d", resp.code)
	}
}

func TestBlockCarriesTheRevision(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	resp := s.get("/p/" + s.id + "/blocks/aaaaaaaa")
	if resp.code != http.StatusOK || resp.header.Get("Kapi-Rev") != "1" {
		t.Errorf("block: %d, Kapi-Rev %q", resp.code, resp.header.Get("Kapi-Rev"))
	}
	if resp := s.get("/p/" + s.id + "/blocks/zzzzzzzz"); resp.code != http.StatusNotFound {
		t.Errorf("unknown block: %d", resp.code)
	}
}

func TestOpsApplyAndLogARevision(t *testing.T) {
	s := newSite(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">see <a href=\"#aaaaaaaa\">a</a></p>\n")
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"done","id":"bbbbbbbb"}}`)
	if code != http.StatusOK || out["rev"] != 2.0 {
		t.Fatalf("done: %d %v", code, out)
	}
	ev := s.events()
	if len(ev) != 1 || ev[0].Author != "human" || ev[0].Kind != "revision" || ev[0].Target != "bbbbbbbb" || ev[0].Text != "marked done" {
		t.Errorf("events = %+v", ev)
	}

	code, out = s.post("/ops", `{"rev":1,"op":{"kind":"delete","id":"bbbbbbbb"}}`)
	if code != http.StatusConflict || out["rev"] != 2.0 {
		t.Errorf("stale op: %d %v, want 409 with rev 2", code, out)
	}
	code, out = s.post("/ops", `{"rev":2,"op":{"kind":"delete","id":"aaaaaaaa"}}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "link") {
		t.Errorf("delete of a linked element: %d %v, want 422", code, out)
	}
	code, out = s.post("/ops", `{"rev":2,"op":{"kind":"undo"}}`)
	if code != http.StatusOK || out["rev"] != 3.0 {
		t.Errorf("undo: %d %v", code, out)
	}
	if b, _ := os.ReadFile(s.path); strings.Contains(string(b), "data-kapi-state") {
		t.Errorf("undo left the file at %s", b)
	}
	code, out = s.post("/ops", `{"rev":3,"op":{"kind":"move","id":"aaaaaaaa"}}`)
	if code != http.StatusOK || out["rev"] != 4.0 {
		t.Errorf("move: %d %v", code, out)
	}
	ev = s.events()
	if len(ev) != 3 {
		t.Fatalf("%d events, want 3: refused ops log nothing", len(ev))
	}
	for _, e := range ev[1:] {
		if e.Target != "" {
			t.Errorf("%s revision has target %q, want none so the browser refetches the page", e.Text, e.Target)
		}
	}
}

func TestAStaleOpAfterAFileEditLogsTheEditAndReturns409(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	editFile(t, s.path, `<p data-kapi-id="aaaaaaaa">a, edited by the agent</p>`)
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"done","id":"aaaaaaaa"}}`)
	if code != http.StatusConflict || out["rev"] != 2.0 {
		t.Fatalf("op over a file edit: %d %v, want 409 rev 2", code, out)
	}
	ev := s.events()
	if len(ev) != 1 || ev[0].Author != "agent" || ev[0].Kind != "revision" || ev[0].Target != "aaaaaaaa" {
		t.Errorf("events = %+v, want one agent revision on #aaaaaaaa", ev)
	}
}

func editFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestMessagesAndAnswersAppendEvents(t *testing.T) {
	s := newSite(t, `<kapi-form data-kapi-id="ffffffff" name="env"><kapi-choice data-kapi-id="cccccccc" name="where"><kapi-opt data-kapi-id="oooooooo" value="prod">Prod</kapi-opt></kapi-choice></kapi-form>`)
	if code, out := s.post("/messages", `{"kind":"message","target":"ffffffff","text":"why prod?"}`); code != http.StatusOK || out["seq"] != 1.0 {
		t.Errorf("message: %d %v", code, out)
	}
	if code, _ := s.post("/messages", `{"kind":"message","text":"  "}`); code != http.StatusUnprocessableEntity {
		t.Errorf("empty message: %d, want 422", code)
	}
	if code, _ := s.post("/messages", `{"kind":"answer","text":"x"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("message with another kind: %d, want 422", code)
	}
	if code, out := s.post("/answers", `{"form":"env","values":{"where":["prod"]}}`); code != http.StatusOK || out["seq"] != 2.0 {
		t.Errorf("answer: %d %v", code, out)
	}
	if code, _ := s.post("/answers", `{"form":"nope","values":{}}`); code != http.StatusUnprocessableEntity {
		t.Errorf("answer to an unknown form: %d, want 422", code)
	}
	if code, _ := s.post("/answers", `not json`); code != http.StatusBadRequest {
		t.Errorf("bad body: %d, want 400", code)
	}
	ev := s.events()
	if len(ev) != 2 || ev[0].Author != "human" || ev[0].Target != "ffffffff" || ev[0].Text != "why prod?" {
		t.Fatalf("events = %+v", ev)
	}
	if ev[1].Kind != "answer" || ev[1].Target != "ffffffff" || ev[1].Text != `{"form":"env","values":{"where":["prod"]}}` {
		t.Errorf("answer event = %+v", ev[1])
	}
}

type sse struct {
	t    *testing.T
	msgs chan [2]string
}

func (s *site) stream(ctx context.Context, lastID string) *sse {
	s.t.Helper()
	r, err := http.NewRequestWithContext(ctx, "GET", s.srv.URL+"/p/"+s.id+"/events", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if lastID != "" {
		r.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := s.client.Do(r) //nolint:bodyclose // the reader goroutine closes it
	if err != nil {
		s.t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || ct != "text/event-stream" {
		s.t.Fatalf("events: %s %q", resp.Status, ct)
	}
	out := &sse{t: s.t, msgs: make(chan [2]string, 16)}
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		var id string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event:") {
				out.msgs <- [2]string{"named", line}
			}
			if v, ok := strings.CutPrefix(line, "id: "); ok {
				id = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				out.msgs <- [2]string{id, v}
			}
		}
		close(out.msgs)
	}()
	return out
}

func (e *sse) next() (string, eventlog.Event) {
	e.t.Helper()
	select {
	case m, ok := <-e.msgs:
		if !ok {
			e.t.Fatal("stream closed")
		}
		var ev eventlog.Event
		if err := json.Unmarshal([]byte(m[1]), &ev); err != nil {
			e.t.Fatal(err)
		}
		return m[0], ev
	case <-time.After(5 * time.Second):
		e.t.Fatal("no event within 5s")
	}
	return "", eventlog.Event{}
}

func TestEventsResumeAfterLastEventID(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, text := range []string{"one", "two", "three"} {
		s.post("/messages", `{"kind":"message","text":"`+text+`"}`)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	all := s.stream(ctx, "")
	if id, ev := all.next(); id != "1" || ev.Text != "one" {
		t.Errorf("fresh stream starts at %s %q, want 1 one", id, ev.Text)
	}

	resumed := s.stream(ctx, "2")
	if id, ev := resumed.next(); id != "3" || ev.Seq != 3 || ev.Text != "three" {
		t.Fatalf("resumed stream starts at %s %+v, want 3", id, ev)
	}
	s.post("/messages", `{"kind":"message","text":"live"}`)
	if id, ev := resumed.next(); id != "4" || ev.Text != "live" {
		t.Errorf("live event = %s %+v", id, ev)
	}

	r, err := http.NewRequest("GET", s.srv.URL+"/p/"+s.id+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Last-Event-ID", "x")
	resp, err := s.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad Last-Event-ID: %s", resp.Status)
	}
}

func TestPollStreamsAFileEditAsARevision(t *testing.T) {
	s := newSite(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.web.Poll(ctx, 20*time.Millisecond)
	events := s.stream(ctx, "")

	editFile(t, s.path, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p>new</p>\n")
	_, ev := events.next()
	if ev.Kind != "revision" || ev.Author != "agent" || ev.Target != "" || ev.Text != "edited in the file" {
		t.Errorf("event = %+v, want an agent revision with no target", ev)
	}
	if page := s.get("/p/" + s.id + "/").body; !strings.Contains(page, `data-rev="2"`) {
		t.Errorf("page after the edit:\n%s", page)
	}
}
