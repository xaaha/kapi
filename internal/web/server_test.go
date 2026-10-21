package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
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
	s := &site{t: t, srv: srv, web: web, pages: pages, path: path, id: id, client: &http.Client{}}
	if resp := s.get("/p/" + id + "/"); resp.code != http.StatusOK {
		t.Fatalf("first load: %d", resp.code)
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
	return s.raw("GET", "/"+token+path, nil)
}

func (s *site) raw(method, path string, header http.Header) got {
	s.t.Helper()
	r, err := http.NewRequest(method, s.srv.URL+path, strings.NewReader("{}"))
	if err != nil {
		s.t.Fatal(err)
	}
	for k, v := range header {
		r.Header[k] = v
	}
	resp, err := s.client.Do(r)
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
	r, err := http.NewRequest("POST", s.srv.URL+"/"+token+"/p/"+s.id+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	r.Header.Set("Origin", s.srv.URL)
	r.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(r)
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

func TestEveryPathNeedsTheTokenAsItsFirstSegment(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, path := range []string{
		"/p/" + s.id + "/",
		"/static/kapi.js",
		"/nope/p/" + s.id + "/",
		"/" + token + "x/p/" + s.id + "/",
		"/p/" + s.id + "/?t=" + token,
		"/x/" + token + "/p/" + s.id + "/",
	} {
		resp := s.raw("GET", path, nil)
		if resp.code != http.StatusForbidden {
			t.Errorf("GET %s: %d, want 403", path, resp.code)
		}
	}
	resp := s.raw("POST", "/nope/p/"+s.id+"/messages", nil)
	if resp.code != http.StatusForbidden {
		t.Errorf("POST with a wrong token: %d, want 403", resp.code)
	}
	if n := len(s.events()); n != 0 {
		t.Errorf("%d events appended by refused requests", n)
	}
}

func TestPostsFromAnotherOriginOrNotJSONAreRefused(t *testing.T) {
	src := `<p data-kapi-id="aaaaaaaa">a</p><kapi-form data-kapi-id="ffffffff" name="env"></kapi-form>`
	s := newSite(t, src)
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(s.srv.Listener.Addr().(*net.TCPAddr).Port)
	bodies := map[string]string{
		"ops":      `{"rev":1,"op":{"kind":"delete","id":"aaaaaaaa"}}`,
		"messages": `{"kind":"message","text":"x"}`,
		"answers":  `{"form":"env","values":{}}`,
	}
	for _, h := range []map[string]string{
		{"Content-Type": "application/json"},
		{"Origin": "http://127.0.0.1:1", "Content-Type": "application/json"},
		{"Origin": "http://localhost:" + port, "Content-Type": "application/json"},
		{"Origin": "null", "Content-Type": "application/json"},
		{"Origin": s.srv.URL, "Content-Type": "text/plain"},
		{"Origin": s.srv.URL},
		{"Origin": s.srv.URL + "/", "Content-Type": "application/json"},
	} {
		for path, body := range bodies {
			r, err := http.NewRequest("POST", s.srv.URL+"/"+token+"/p/"+s.id+"/"+path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range h {
				r.Header.Set(k, v)
			}
			resp, err := s.client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("POST %s with %v: %s, want 403", path, h, resp.Status)
			}
		}
	}
	if after, _ := os.ReadFile(s.path); !bytes.Equal(after, before) {
		t.Errorf("a refused op changed the file:\n%s", after)
	}
	if n := len(s.events()); n != 0 {
		t.Errorf("%d events appended by refused posts", n)
	}
	if code, _ := s.post("/messages", `{"kind":"message","text":"same origin"}`); code != http.StatusOK {
		t.Errorf("same-origin JSON post: %d", code)
	}
}

func TestResponsesSetNoCookieAndNoReferrer(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, resp := range []got{s.get("/p/" + s.id + "/"), s.get("/static/kapi.js"), s.raw("GET", "/nope/", nil)} {
		if c := resp.header.Values("Set-Cookie"); len(c) != 0 {
			t.Errorf("%d response sets cookies %v", resp.code, c)
		}
		if rp := resp.header.Get("Referrer-Policy"); rp != "no-referrer" {
			t.Errorf("%d response has Referrer-Policy %q", resp.code, rp)
		}
	}
}

func TestAnotherHostIsRefusedEvenWithTheToken(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, host := range []string{"evil.example:80", "localhost:" + strconv.Itoa(s.srv.Listener.Addr().(*net.TCPAddr).Port)} {
		r, err := http.NewRequest("GET", s.srv.URL+"/"+token+"/p/"+s.id+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = host
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Host %s: %s, want 403", host, resp.Status)
		}
	}
}

func TestPageServesTheRenderedBodyWithRevisionAndAssets(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">hello &amp; welcome</p>`)
	page := s.get("/p/" + s.id + "/").body
	for _, want := range []string{`<main id="kapi-page" data-rev="1">`, `hello &amp; welcome`, `<link rel="stylesheet" href="../../static/chrome.css">`, `<script src="../../static/kapi.js" defer></script>`, `<title>plan.html</title>`} {
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

func TestSourceIsTheElementsOuterHTMLAsText(t *testing.T) {
	s := newSite(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<kapi-code data-kapi-id=\"bbbbbbbb\" lang=\"go\">if a &lt; b {}</kapi-code>\n")
	resp := s.get("/p/" + s.id + "/source/bbbbbbbb")
	if resp.code != http.StatusOK || resp.header.Get("Kapi-Rev") != "1" || !strings.HasPrefix(resp.header.Get("Content-Type"), "text/plain") {
		t.Fatalf("source: %d, Kapi-Rev %q, Content-Type %q", resp.code, resp.header.Get("Kapi-Rev"), resp.header.Get("Content-Type"))
	}
	if want := `<kapi-code data-kapi-id="bbbbbbbb" lang="go">if a &lt; b {}</kapi-code>`; resp.body != want {
		t.Errorf("source = %q, want %q", resp.body, want)
	}
	if resp := s.get("/p/" + s.id + "/source/zzzzzzzz"); resp.code != http.StatusNotFound {
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
	for name, edit := range map[string]func(t *testing.T, path, src string){"new mtime": editFile, "same size and mtime": editKeepingStamp} {
		t.Run(name, func(t *testing.T) {
			s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
			edit(t, s.path, `<p data-kapi-id="aaaaaaaa">b</p>`)
			code, out := s.post("/ops", `{"rev":1,"op":{"kind":"done","id":"aaaaaaaa"}}`)
			if code != http.StatusConflict || out["rev"] != 2.0 {
				t.Fatalf("op over a file edit: %d %v, want 409 rev 2", code, out)
			}
			if b, _ := os.ReadFile(s.path); string(b) != `<p data-kapi-id="aaaaaaaa">b</p>` {
				t.Errorf("file = %s, want the agent's edit kept", b)
			}
			ev := s.events()
			if len(ev) != 1 || ev[0].Author != "agent" || ev[0].Kind != "revision" || ev[0].Target != "aaaaaaaa" {
				t.Errorf("events = %+v, want one agent revision on #aaaaaaaa", ev)
			}
		})
	}
}

func editKeepingStamp(t *testing.T, path, src string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
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
	r, err := http.NewRequestWithContext(ctx, "GET", s.srv.URL+"/"+token+"/p/"+s.id+"/events", nil)
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

	r, err := http.NewRequest("GET", s.srv.URL+"/"+token+"/p/"+s.id+"/events", nil)
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
