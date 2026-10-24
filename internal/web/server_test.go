package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/render"
)

const (
	token    = "s3cret"
	oldToken = "0123456789abcdef0123456789abcdef"
)

type fakePages struct {
	mu    sync.Mutex
	home  string
	paths map[string]string
	logs  map[string]*eventlog.Log
	pages map[string]*page.Store
}

func (f *fakePages) Viewing(string) func() { return func() {} }

func (f *fakePages) Reopened(string) bool { return false }

func (f *fakePages) PagePath(id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, ok := f.paths[id]
	if !ok {
		return "", fs.ErrNotExist
	}
	return path, nil
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
	web := New(pages, token, srv.Listener.Addr().String(), [][32]byte{sha256.Sum256([]byte(oldToken))})
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

func TestAnOldTokenOnAPageGetsARestartNotice(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	const old = oldToken
	const never = "fedcba9876543210fedcba9876543210"
	for _, tc := range []struct{ path, reopen string }{
		{"/" + old + "/p/" + s.id + "/", "kapi open " + s.path},
		{"/" + old + "/p/" + s.id + "/events", "kapi open " + s.path},
		{"/" + old + "/p/0123456789abcdef/", "kapi open <page>"},
		{"/" + never + "/p/" + s.id + "/", "kapi open <page>"},
		{"/" + never + "/p/" + s.id + "/events", "kapi open <page>"},
		{"/x/p/" + s.id + "/", "kapi open <page>"},
	} {
		resp := s.raw("GET", tc.path, nil)
		var body map[string]string
		if err := json.Unmarshal([]byte(resp.body), &body); err != nil {
			t.Fatalf("GET %s: %d %q is not JSON", tc.path, resp.code, resp.body)
		}
		if resp.code != http.StatusForbidden || body["error"] != "kapi restarted" || body["reopen"] != tc.reopen || len(body) != 2 {
			t.Errorf("GET %s: %d %v, want 403 with reopen %q", tc.path, resp.code, body, tc.reopen)
		}
		if strings.Contains(resp.body, token) || strings.Contains(resp.body, old) || strings.Contains(resp.body, never) {
			t.Errorf("GET %s: body %q carries a token", tc.path, resp.body)
		}
	}
	for _, p := range []string{"/" + old + "/static/kapi.js", "/" + old + "/p/" + s.id + "/blocks/aaaaaaaa"} {
		if resp := s.raw("GET", p, nil); resp.code != http.StatusForbidden || strings.Contains(resp.body, "restarted") {
			t.Errorf("GET %s: %d %q, want a plain 403", p, resp.code, resp.body)
		}
	}
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

const pageCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"

func TestPageStripsAndBlocksSourceThatOverridesTheReferrerPolicy(t *testing.T) {
	payload := `<p data-kapi-id="aaaaaaaa"><img referrerpolicy="unsafe-url" src="http://127.0.0.1:61999/x"></p>` + "\n" + `<meta name="referrer" content="unsafe-url">`
	s := newSite(t, payload)
	resp := s.get("/p/" + s.id + "/")
	for _, leak := range []string{"referrerpolicy", `name="referrer"`, "unsafe-url"} {
		if strings.Contains(resp.body, leak) {
			t.Errorf("page still contains %q:\n%s", leak, resp.body)
		}
	}
	if got := resp.header.Get("Content-Security-Policy"); got != pageCSP {
		t.Errorf("page CSP = %q, want %q", got, pageCSP)
	}
}

func TestUntidyPathsAreRefusedBeforeTheTokenIsStripped(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, p := range []string{
		"/" + token + "//p/" + s.id + "/",
		"/" + token + "/p/" + s.id + "//",
		"/" + token + "/p/" + s.id + "/../" + s.id + "/",
		"/" + token + "/p/./" + s.id + "/",
		"/" + token + "/p/" + s.id + "/%2e%2e/" + s.id + "/",
		"//" + token + "/p/" + s.id + "/",
	} {
		conn, err := net.Dial("tcp", s.srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", p, s.srv.Listener.Addr())
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		conn.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s: %s (Location %q), want 400", p, resp.Status, resp.Header.Get("Location"))
		}
	}
	if resp := s.get("/p/" + s.id + "/"); resp.code != http.StatusOK {
		t.Errorf("tidy path: %d", resp.code)
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
	for _, want := range []string{`<main id="kapi-page" data-rev="1" data-undo="false">`, `hello &amp; welcome`, `<link rel="stylesheet" href="../../static/chrome.css">`, `<script src="../../static/kapi.js" defer></script>`, `<title>plan.html</title>`, `<link rel="icon" type="image/svg+xml" href="../../static/favicon.svg">`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	for _, p := range []string{"/static/kapi.js", "/static/chrome.css", "/static/logo.svg", "/static/favicon.svg"} {
		if resp := s.get(p); resp.code != http.StatusOK {
			t.Errorf("GET %s: %d", p, resp.code)
		}
	}
	if resp := s.get("/p/0123456789abcdef/"); resp.code != http.StatusNotFound {
		t.Errorf("unknown page: %d", resp.code)
	}
	for _, f := range []string{"hanken-grotesk", "hanken-grotesk-italic", "source-serif-4", "source-serif-4-italic"} {
		resp := s.get("/static/fonts/" + f + ".woff2")
		if resp.code != http.StatusOK || resp.header.Get("Content-Type") != "font/woff2" || !strings.HasPrefix(resp.body, "wOF2") {
			t.Errorf("GET font %s: %d %q", f, resp.code, resp.header.Get("Content-Type"))
		}
	}
	chrome := s.get("/static/chrome.css").body
	for _, want := range []string{`font-family: "Hanken Grotesk"`, `url("fonts/hanken-grotesk.woff2")`, `font-family: "Source Serif 4"`, `url("fonts/source-serif-4.woff2")`} {
		if !strings.Contains(chrome, want) {
			t.Errorf("chrome.css lacks %s", want)
		}
	}
}

func TestThemeScriptRunsBeforeAnyStyle(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	page := s.get("/p/" + s.id + "/").body
	head, _, ok := strings.Cut(page, "</head>")
	if !ok {
		t.Fatalf("page has no head:\n%s", page)
	}
	script := strings.Index(head, `<script src="../../static/theme.js"></script>`)
	if script < 0 {
		t.Fatalf("head lacks a blocking theme.js script:\n%s", head)
	}
	for _, later := range []string{"<style>", `<link rel="stylesheet"`} {
		if i := strings.Index(head, later); i < script {
			t.Errorf("%s comes before theme.js (at %d, theme.js at %d)", later, i, script)
		}
	}
	resp := s.get("/static/theme.js")
	if resp.code != http.StatusOK || !strings.Contains(resp.header.Get("Content-Type"), "javascript") {
		t.Fatalf("GET /static/theme.js: %d %q", resp.code, resp.header.Get("Content-Type"))
	}
	if !strings.Contains(resp.body, "prefers-color-scheme: dark") || !strings.Contains(resp.body, "localStorage") {
		t.Errorf("theme.js does not look like the theme script:\n%s", resp.body)
	}
}

var cssVar = regexp.MustCompile(`var\((--kapi-[a-z0-9-]+)`)
var cssDef = regexp.MustCompile(`(--kapi-[a-z0-9-]+)\s*:`)

func TestEveryColourTokenIsDefined(t *testing.T) {
	chrome, err := static.ReadFile("static/chrome.css")
	if err != nil {
		t.Fatal(err)
	}
	all := string(render.CSS()) + string(chrome)
	defined := map[string]bool{"--kapi-bar-height": true}
	for _, m := range cssDef.FindAllStringSubmatch(all, -1) {
		defined[m[1]] = true
	}
	for _, m := range cssVar.FindAllStringSubmatch(all, -1) {
		if !defined[m[1]] {
			t.Errorf("%s is used but never defined", m[1])
		}
	}
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(string(chrome), -1); len(hex) > 0 {
		t.Errorf("chrome.css hard-codes colours %v; use the theme tokens", hex)
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

func TestInnerSourceIsTheBodyByteForByte(t *testing.T) {
	s := newSite(t, "<kapi-callout data-kapi-id=\"aaaaaaaa\" kind=\"note\">a <em>b</em></kapi-callout>\n<kapi-code data-kapi-id=\"bbbbbbbb\" lang=\"go\">if a < b && c {\n}</kapi-code>\n")
	for id, want := range map[string]string{"aaaaaaaa": "a <em>b</em>", "bbbbbbbb": "if a < b && c {\n}"} {
		resp := s.get("/p/" + s.id + "/source/" + id + "?part=inner")
		if resp.code != http.StatusOK || resp.header.Get("Kapi-Rev") != "1" || !strings.HasPrefix(resp.header.Get("Content-Type"), "text/plain") {
			t.Fatalf("inner source: %d, Kapi-Rev %q, Content-Type %q", resp.code, resp.header.Get("Kapi-Rev"), resp.header.Get("Content-Type"))
		}
		if resp.body != want {
			t.Errorf("inner source of #%s = %q, want %q", id, resp.body, want)
		}
	}
	if resp := s.get("/p/" + s.id + "/source/aaaaaaaa?part=outer"); resp.code != http.StatusBadRequest {
		t.Errorf("unknown part: %d", resp.code)
	}
}

func TestAnInnerEditKeepsTheElementsTags(t *testing.T) {
	s := newSite(t, "<kapi-callout data-kapi-id=\"aaaaaaaa\" kind=\"note\">old</kapi-callout>\n")
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"edit","id":"aaaaaaaa","inner":"new <strong>text</strong>"}}`)
	if code != http.StatusOK || out["rev"] != 2.0 {
		t.Fatalf("inner edit: %d %v", code, out)
	}
	if resp := s.get("/p/" + s.id + "/source/aaaaaaaa"); resp.body != `<kapi-callout data-kapi-id="aaaaaaaa" kind="note">new <strong>text</strong></kapi-callout>` {
		t.Errorf("source after inner edit = %q", resp.body)
	}
	code, out = s.post("/ops", `{"rev":2,"op":{"kind":"edit","id":"aaaaaaaa","inner":"x</kapi-callout><p>y</p><kapi-callout>"}}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(out["error"]), "closes the element early") {
		t.Errorf("escaping inner edit: %d %v", code, out)
	}
}

func TestAnUnchangedEditLogsNothing(t *testing.T) {
	s := newSite(t, "<kapi-callout data-kapi-id=\"aaaaaaaa\" kind=\"note\">same</kapi-callout>\n")
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"edit","id":"aaaaaaaa","inner":"same"}}`)
	if code != http.StatusOK || out["rev"] != 1.0 {
		t.Fatalf("unchanged edit: %d %v, want 200 with rev 1", code, out)
	}
	if ev := s.events(); len(ev) != 0 {
		t.Errorf("events = %+v, want none", ev)
	}
}

func TestATextOpSetsACell(t *testing.T) {
	s := newSite(t, "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><th>a</th></tr><tr><td class=\"x\">b</td></tr></table></kapi-table>\n")
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"text","id":"aaaaaaaa","parts":[{"id":"aaaaaaaa","row":1,"col":0,"inner":"&lt;c&gt; <code>d</code>"}]}}`)
	if code != http.StatusOK || out["rev"] != 2.0 {
		t.Fatalf("text: %d %v", code, out)
	}
	if resp := s.get("/p/" + s.id + "/source/aaaaaaaa"); resp.body != `<kapi-table data-kapi-id="aaaaaaaa"><table><tr><th>a</th></tr><tr><td class="x">&lt;c&gt; <code>d</code></td></tr></table></kapi-table>` {
		t.Errorf("source after text op = %q", resp.body)
	}
	if code, out := s.post("/ops", `{"rev":2,"op":{"kind":"text","id":"aaaaaaaa","parts":[{"id":"aaaaaaaa","inner":"x"}]}}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a text op on the table itself: %d %v, want 422", code, out)
	}
	if ev := s.events(); len(ev) != 1 || ev[0].Target != "aaaaaaaa" {
		t.Errorf("events = %+v", ev)
	}
}

func TestOpsApplyAndLogARevision(t *testing.T) {
	s := newSite(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">see <a href=\"#aaaaaaaa\">a</a></p>\n")
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"archive","id":"bbbbbbbb"}}`)
	if code != http.StatusOK || out["rev"] != 2.0 {
		t.Fatalf("archive: %d %v", code, out)
	}
	ev := s.events()
	if len(ev) != 1 || ev[0].Author != "human" || ev[0].Kind != "revision" || ev[0].Target != "bbbbbbbb" || ev[0].Text != `archived "see a"` {
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
			code, out := s.post("/ops", `{"rev":1,"op":{"kind":"archive","id":"aaaaaaaa"}}`)
			if code != http.StatusConflict || out["rev"] != 2.0 {
				t.Fatalf("op over a file edit: %d %v, want 409 rev 2", code, out)
			}
			if b, _ := os.ReadFile(s.path); string(b) != `<p data-kapi-id="aaaaaaaa">b</p>` {
				t.Errorf("file = %s, want the agent's edit kept", b)
			}
			ev := s.events()
			if len(ev) != 1 || ev[0].Author != "file" || ev[0].Kind != "revision" || ev[0].Target != "aaaaaaaa" {
				t.Errorf("events = %+v, want one file revision on #aaaaaaaa", ev)
			}
		})
	}
}

func TestAnEditThatLeavesADanglingLinkIs422(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"edit","id":"aaaaaaaa","html":"<p><a href=\"#zzzzzzzz\">z</a></p>"}}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(out["error"]), "#zzzzzzzz") {
		t.Errorf("edit adding a dangling link: %d %v, want 422 naming #zzzzzzzz", code, out)
	}
}

func TestAnOpWithoutAnIDIs422(t *testing.T) {
	s := newSite(t, `<h1 data-kapi-id="hhhhhhhh">Plan <b>bold</b></h1>`)
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"edit","id":"","html":"<b>x</b>"}}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(out["error"]), "id") {
		t.Errorf("edit with no id: %d %v, want 422", code, out)
	}
	if b, _ := os.ReadFile(s.path); strings.Contains(string(b), `data-kapi-id=""`) {
		t.Errorf("file = %s", b)
	}
}

func TestASaveIsNotRefusedWhenOnlyAnotherBlockChanged(t *testing.T) {
	s := newSite(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	editFile(t, s.path, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b, by the agent</p>\n")
	if code, out := s.post("/ops", `{"rev":1,"op":{"kind":"edit","id":"aaaaaaaa","html":"<p>a, by the human</p>"}}`); code != http.StatusOK || out["rev"] != 3.0 {
		t.Errorf("save on an untouched block: %d %v, want 200 rev 3", code, out)
	}
	if code, out := s.post("/ops", `{"rev":1,"op":{"kind":"archive","id":"bbbbbbbb"}}`); code != http.StatusConflict || out["rev"] != 3.0 {
		t.Errorf("op on the block the agent changed: %d %v, want 409 rev 3", code, out)
	}
}

func TestPageAndOpsSayWhetherThereIsAnythingToUndo(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	if page := s.get("/p/" + s.id + "/").body; !strings.Contains(page, `data-undo="false"`) {
		t.Fatalf("fresh page should offer no undo: %.300s", page)
	}
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"archive","id":"aaaaaaaa"}}`)
	if code != http.StatusOK || out["undo"] != true {
		t.Fatalf("archive: %d %v, want undo true", code, out)
	}
	if page := s.get("/p/" + s.id + "/").body; !strings.Contains(page, `data-undo="true"`) {
		t.Errorf("page after an op should offer undo: %.300s", page)
	}
	if resp := s.get("/p/" + s.id + "/blocks/aaaaaaaa"); resp.header.Get("Kapi-Undo") != "true" {
		t.Errorf("block Kapi-Undo = %q, want true", resp.header.Get("Kapi-Undo"))
	}
	code, out = s.post("/ops", `{"rev":2,"op":{"kind":"undo"}}`)
	if code != http.StatusOK || out["undo"] != false {
		t.Errorf("undo: %d %v, want undo false", code, out)
	}
}

func TestUndoAcrossAFileEditIs422(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	if code, _ := s.post("/ops", `{"rev":1,"op":{"kind":"archive","id":"aaaaaaaa"}}`); code != http.StatusOK {
		t.Fatalf("archive: %d", code)
	}
	editFile(t, s.path, `<p data-kapi-id="aaaaaaaa" data-kapi-state="archived">a, by the agent</p>`)
	code, out := s.post("/ops", `{"rev":3,"op":{"kind":"undo"}}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(out["error"]), "changed outside the browser") {
		t.Errorf("undo across a file edit: %d %v, want 422", code, out)
	}
	if b, _ := os.ReadFile(s.path); !strings.Contains(string(b), "by the agent") {
		t.Errorf("file = %s, want the agent's edit kept", b)
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
			if line == "" {
				id = ""
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				out.msgs <- [2]string{id, v}
			}
		}
		close(out.msgs)
	}()
	return out
}

func (e *sse) raw() (id, data string) {
	e.t.Helper()
	select {
	case m, ok := <-e.msgs:
		if !ok {
			e.t.Fatal("stream closed")
		}
		return m[0], m[1]
	case <-time.After(5 * time.Second):
		e.t.Fatal("no message within 5s")
	}
	return "", ""
}

func TestTheStreamSendsReadPositionsWithoutAnID(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s.post("/messages", `{"kind":"message","text":"one"}`)
	s.post("/messages", `{"kind":"message","text":"two"}`)
	_, l, err := s.pages.Page(s.id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	live := s.stream(ctx, "")
	for _, want := range []string{"1", "2"} {
		if id, _ := live.raw(); id != want {
			t.Fatalf("event id %q, want %s", id, want)
		}
	}
	if err := l.Ack("agent", 1); err != nil {
		t.Fatal(err)
	}
	if id, data := live.raw(); id != "" || data != `{"kind":"read","reader":"agent","seq":1}` {
		t.Fatalf("after an ack: id %q data %s, want a read message with no id", id, data)
	}
	s.post("/messages", `{"kind":"message","text":"three"}`)
	if id, _ := live.raw(); id != "3" {
		t.Fatalf("event after the read message has id %q, want 3", id)
	}

	again := s.stream(ctx, "3")
	if id, data := again.raw(); id != "" || data != `{"kind":"read","reader":"agent","seq":1}` {
		t.Fatalf("a reconnect starts with id %q data %s, want the current read position", id, data)
	}
}

func TestTheStreamSendsTheQueuedPositionApartFromTheReadOne(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s.post("/messages", `{"kind":"message","text":"one"}`)
	s.post("/messages", `{"kind":"message","text":"two"}`)
	_, l, err := s.pages.Page(s.id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live := s.stream(ctx, "2")
	if err := l.Queue("agent", 2); err != nil {
		t.Fatal(err)
	}
	if id, data := live.raw(); id != "" || data != `{"kind":"queued","reader":"agent","seq":2}` {
		t.Fatalf("after a queue: id %q data %s, want a queued message with no id", id, data)
	}
	if err := l.Ack("agent", 1); err != nil {
		t.Fatal(err)
	}
	if _, data := live.raw(); data != `{"kind":"read","reader":"agent","seq":1}` {
		t.Fatalf("after an ack: data %s, want the read position alone", data)
	}

	again := s.stream(ctx, "2")
	for _, want := range []string{`{"kind":"queued","reader":"agent","seq":2}`, `{"kind":"read","reader":"agent","seq":1}`} {
		if _, data := again.raw(); data != want {
			t.Fatalf("a reconnect sent %s, want %s", data, want)
		}
	}
}

func TestReservedReadersSendNoReadPosition(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s.post("/messages", `{"kind":"message","text":"one"}`)
	_, l, err := s.pages.Page(s.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"human", "kapi", "agent"} {
		if err := l.Ack(r, 1); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := s.stream(ctx, "1")
	if _, data := st.raw(); data != `{"kind":"read","reader":"agent","seq":1}` {
		t.Errorf("first read message = %s, want only the agent's", data)
	}
	s.post("/messages", `{"kind":"message","text":"two"}`)
	if id, _ := st.raw(); id != "2" {
		t.Errorf("next message has id %q, want the event 2 and no read position for human or kapi", id)
	}
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
	if ev.Kind != "revision" || ev.Author != "file" || ev.Target != "" || ev.Text != "edited in the file" {
		t.Errorf("event = %+v, want a file revision with no target", ev)
	}
	if page := s.get("/p/" + s.id + "/").body; !strings.Contains(page, `data-rev="2"`) {
		t.Errorf("page after the edit:\n%s", page)
	}
}
