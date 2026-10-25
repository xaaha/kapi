package publish

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const token = "tok-9f3a-very-secret"

type hub struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	statuses []int
	body     string
	hijack   int
}

func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	h.requests = append(h.requests, r)
	h.bodies = append(h.bodies, string(b))
	if h.hijack > 0 {
		h.hijack--
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
		return
	}
	status := http.StatusCreated
	if len(h.statuses) > 0 {
		status, h.statuses = h.statuses[0], h.statuses[1:]
	}
	if status >= 300 && status < 400 {
		w.Header().Set("Location", "/login")
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, h.body)
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func newClient(t *testing.T, h *hub) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(Config{URL: srv.URL, Token: token})
	var waits []time.Duration
	c.wait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return c, &waits
}

const pageID = "0123456789abcdef"

func TestPublishPutsTheRenderedDocument(t *testing.T) {
	h := &hub{}
	c, _ := newClient(t, h)
	got, err := c.Publish(context.Background(), pageID, 42, "plan <v2>.html", []byte("<p>hello</p>"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := c.cfg.URL + "/p/" + pageID + "/"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
	if h.count() != 1 {
		t.Fatalf("requests = %d, want 1", h.count())
	}
	r := h.requests[0]
	if r.Method != http.MethodPut || r.URL.Path != "/api/pages/"+pageID+"/revs/42" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
	}
	if got := r.Header.Get("Kapi-Title"); got != "plan%20%3Cv2%3E.html" {
		t.Errorf("Kapi-Title = %q", got)
	}
	doc, _ := Document("plan <v2>.html", []byte("<p>hello</p>"), t.TempDir())
	if h.bodies[0] != string(doc) {
		t.Errorf("body = %q, want %q", h.bodies[0], doc)
	}
}

func TestDocumentIsSelfContained(t *testing.T) {
	doc, err := Document("a <b> & c", []byte("<p>hi</p>"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := string(doc)
	for _, want := range []string{"<!doctype html>", "<title>a &lt;b&gt; &amp; c</title>", "<style>", `<main id="kapi-page">`, "<p>hi</p>"} {
		if !strings.Contains(s, want) {
			t.Errorf("document lacks %q:\n%s", want, s)
		}
	}
	for _, unwanted := range []string{"<script", "<link", "kapi.js"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("document contains %q", unwanted)
		}
	}
}

func TestPutRetriesServerErrorsWithBackoff(t *testing.T) {
	h := &hub{statuses: []int{500, 503, 429, 408}}
	c, waits := newClient(t, h)
	if err := c.Put(context.Background(), pageID, 1, "t", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if h.count() != 5 {
		t.Errorf("requests = %d, want 5", h.count())
	}
	want := defaultBackoff[:4]
	if fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v", *waits, want)
	}
	for i, b := range h.bodies {
		if b != "x" {
			t.Errorf("attempt %d body = %q", i+1, b)
		}
	}
}

func TestPutRetriesDroppedConnections(t *testing.T) {
	h := &hub{hijack: 2}
	c, _ := newClient(t, h)
	if err := c.Put(context.Background(), pageID, 1, "t", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if h.count() != 3 {
		t.Errorf("requests = %d, want 3", h.count())
	}
}

func TestPutGivesUpAfterTheLastAttempt(t *testing.T) {
	h := &hub{statuses: []int{502, 502, 502, 502, 502, 502}, body: "bad gateway"}
	c, _ := newClient(t, h)
	err := c.Put(context.Background(), pageID, 1, "t", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "bad gateway") {
		t.Fatalf("err = %v", err)
	}
	if want := len(defaultBackoff) + 1; h.count() != want {
		t.Errorf("requests = %d, want %d", h.count(), want)
	}
}

func TestPutDoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 413, 415} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := &hub{statuses: []int{status, 201}}
			c, waits := newClient(t, h)
			err := c.Put(context.Background(), pageID, 1, "t", []byte("x"))
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Fatalf("err = %v", err)
			}
			if h.count() != 1 || len(*waits) != 0 {
				t.Errorf("requests = %d, waits = %d, want 1 and 0", h.count(), len(*waits))
			}
		})
	}
}

func TestPutDoesNotFollowRedirects(t *testing.T) {
	h := &hub{statuses: []int{302}}
	c, _ := newClient(t, h)
	err := c.Put(context.Background(), pageID, 1, "t", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("err = %v", err)
	}
	if h.count() != 1 {
		t.Errorf("requests = %d, want 1", h.count())
	}
}

func TestPutStopsWhenTheContextIsCancelled(t *testing.T) {
	h := &hub{statuses: []int{503, 503, 503, 503, 503}}
	c, _ := newClient(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	c.wait = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	if err := c.Put(ctx, pageID, 1, "t", []byte("x")); err == nil {
		t.Fatal("want error")
	}
	if h.count() != 1 {
		t.Errorf("requests = %d, want 1", h.count())
	}
}

func TestPutRejectsMalformedPageIDs(t *testing.T) {
	h := &hub{}
	c, _ := newClient(t, h)
	for _, id := range []string{"", "../../etc", "0123456789ABCDEF", "0123456789abcde"} {
		if err := c.Put(context.Background(), id, 1, "t", nil); err == nil {
			t.Errorf("id %q: want error", id)
		}
	}
	if h.count() != 0 {
		t.Errorf("requests = %d, want 0", h.count())
	}
}

func TestTokenNeverAppearsInErrors(t *testing.T) {
	cases := map[string]*hub{
		"echoing 401": {statuses: []int{401}, body: "bad token Bearer " + token},
		"echoing 500": {statuses: []int{500, 500, 500, 500, 500}, body: token},
		"dropped":     {hijack: 5},
		"redirect":    {statuses: []int{302}},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := newClient(t, h)
			err := c.Put(context.Background(), pageID, 1, "t", []byte("x"))
			if err == nil {
				t.Fatal("want error")
			}
			if strings.Contains(err.Error(), token) || strings.Contains(fmt.Sprintf("%+v %#v", err, err), token) {
				t.Errorf("error leaks token: %v", err)
			}
		})
	}
}

func TestTokenIsRedactedWhenFormatted(t *testing.T) {
	cfg := Config{URL: "https://hub.example", Token: token}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		if s := fmt.Sprintf(verb, cfg); strings.Contains(s, token) || strings.Contains(s, fmt.Sprintf("%x", token)) {
			t.Errorf("%s leaks the token: %s", verb, s)
		}
		if s := fmt.Sprintf(verb, cfg.Token); strings.Contains(s, token) {
			t.Errorf("%s of Token leaks it: %s", verb, s)
		}
	}
}

func TestLoadConfigFromHubJSON(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "hub-token"), "  "+token+"\n")
	write(t, filepath.Join(home, "hub.json"), `{"url":"https://hub.example/","token_file":"hub-token"}`)
	cfg, err := LoadConfig(home, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "https://hub.example" || string(cfg.Token) != token {
		t.Errorf("cfg = %q, %q", cfg.URL, string(cfg.Token))
	}

	abs := filepath.Join(t.TempDir(), "elsewhere")
	write(t, abs, token)
	write(t, filepath.Join(home, "hub.json"), `{"url":"https://hub.example","token_file":"`+abs+`"}`)
	if cfg, err = LoadConfig(home, env(nil)); err != nil || string(cfg.Token) != token {
		t.Errorf("absolute token_file: %v, %v", err, cfg)
	}
}

func TestLoadConfigEnvironmentWins(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "hub.json"), `{"url":"https://file.example","token_file":"missing"}`)
	cfg, err := LoadConfig(home, env(map[string]string{"KAPI_HUB_URL": "https://env.example", "KAPI_HUB_TOKEN": token}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "https://env.example" || string(cfg.Token) != token {
		t.Errorf("cfg = %q", cfg.URL)
	}
}

func TestLoadConfigFailures(t *testing.T) {
	cases := []struct {
		name    string
		hubJSON string
		env     map[string]string
		want    string
	}{
		{"nothing", "", nil, ErrNotConfigured.Error()},
		{"no token", "", map[string]string{"KAPI_HUB_URL": "https://h.example"}, "token missing"},
		{"no url", "", map[string]string{"KAPI_HUB_TOKEN": token}, "url missing"},
		{"plain http", "", map[string]string{"KAPI_HUB_URL": "http://h.example", "KAPI_HUB_TOKEN": token}, "must use https"},
		{"not a url", "", map[string]string{"KAPI_HUB_URL": "h.example", "KAPI_HUB_TOKEN": token}, "must look like"},
		{"missing token file", `{"url":"https://h.example","token_file":"nope"}`, nil, "token file"},
		{"empty token file", `{"url":"https://h.example","token_file":"empty"}`, nil, "is empty"},
		{"inline token", `{"url":"https://h.example","token":"` + token + `"}`, nil, "unknown field"},
		{"broken json", `{`, nil, "hub.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			write(t, filepath.Join(home, "empty"), "\n")
			if tc.hubJSON != "" {
				write(t, filepath.Join(home, "hub.json"), tc.hubJSON)
			}
			_, err := LoadConfig(home, env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("error leaks token: %v", err)
			}
		})
	}
	if _, err := LoadConfig(t.TempDir(), env(nil)); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}

func TestLoadConfigAllowsPlainHTTPOnLoopback(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8787", "http://localhost:8787", "http://[::1]:8787"} {
		if _, err := LoadConfig(t.TempDir(), env(map[string]string{"KAPI_HUB_URL": u, "KAPI_HUB_TOKEN": token})); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestPublishingEmbedsThePagesImagesAndRefusesAPageOverTheHubLimit(t *testing.T) {
	dir := t.TempDir()
	files := filepath.Join(dir, "plan.files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	png := "0123456789abcdef0123456789abcdef.png"
	if err := os.WriteFile(filepath.Join(files, png), []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := `<figure data-kapi-id="aaaaaaaa"><img src="./plan.files/` + png + `" alt=""></figure>` +
		`<p data-kapi-id="cccccccc"><img src="https://example.com/x.png" alt=""></p>`
	doc, err := Document("plan.html", []byte(src), files)
	if err != nil {
		t.Fatal(err)
	}
	want := `src="data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("PNGDATA")) + `"`
	if !strings.Contains(string(doc), want) {
		t.Errorf("the page image was not embedded:\n%s", doc)
	}
	if !strings.Contains(string(doc), `src="https://example.com/x.png"`) {
		t.Errorf("an outside image changed:\n%s", doc)
	}
	big := "fedcba9876543210fedcba9876543210.jpg"
	if err := os.WriteFile(filepath.Join(files, big), bytes.Repeat([]byte{0xff}, 9<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	src += `<figure data-kapi-id="dddddddd"><img src="./plan.files/` + big + `" alt=""></figure>`
	src += `<figure data-kapi-id="eeeeeeee"><img src="./plan.files/` + big + `" alt=""></figure>`
	_, err = Document("plan.html", []byte(src), files)
	if err == nil || !strings.Contains(err.Error(), "over the hub's 10 MB limit") || strings.Count(err.Error(), big) != 1 || !strings.Contains(err.Error(), big+" (9.0 MB)") {
		t.Errorf("an oversized page with the image twice: %v", err)
	}

	code := `<pre data-kapi-id="ffffffff"><code>&lt;img src="./plan.files/` + png + `"&gt;</code></pre>` +
		`<p data-kapi-id="gggggggg">Write src="./plan.files/` + png + `" in the tag.</p>`
	doc, err = Document("plan.html", []byte(code), files)
	if err != nil || strings.Contains(string(doc), "data:image") {
		t.Errorf("text that looks like an image src was embedded: %v\n%s", err, doc)
	}
	notDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notDir, "plan.files"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Document("plan.html", []byte(`<figure data-kapi-id="aaaaaaaa"><img src="./plan.files/`+png+`" alt=""></figure>`), filepath.Join(notDir, "plan.files")); err == nil || !strings.Contains(err.Error(), png) {
		t.Errorf("a files folder that is not a folder: %v, want the image named as missing", err)
	}
}

func TestExportEmbedsImagesPastTheHubLimit(t *testing.T) {
	files := filepath.Join(t.TempDir(), "plan.files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	big := "fedcba9876543210fedcba9876543210.jpg"
	data := bytes.Repeat([]byte{0xff}, 9<<20)
	if err := os.WriteFile(filepath.Join(files, big), data, 0o600); err != nil {
		t.Fatal(err)
	}
	src := []byte(`<figure data-kapi-id="aaaaaaaa"><img src="./plan.files/` + big + `" alt=""></figure>`)
	if _, err := Document("plan.html", src, files); err == nil {
		t.Fatal("Document took a page over the hub limit")
	}
	doc, err := Export("plan.html", src, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) <= MaxDocBytes || !strings.Contains(string(doc), `src="data:image/jpeg;base64,`+base64.StdEncoding.EncodeToString(data)+`"`) {
		t.Errorf("the export is %d bytes and does not carry the image", len(doc))
	}
}

func TestAPageWithMissingImagesIsRefusedAndTheErrorNamesEachOnce(t *testing.T) {
	files := filepath.Join(t.TempDir(), "plan.files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	here := "0123456789abcdef0123456789abcdef.png"
	if err := os.WriteFile(filepath.Join(files, here), []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	gone := "ffffffffffffffffffffffffffffffff.png"
	alsoGone := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.webp"
	src := []byte(`<figure data-kapi-id="aaaaaaaa"><img src="./plan.files/` + here + `" alt=""></figure>` +
		`<figure data-kapi-id="bbbbbbbb"><img src="./plan.files/` + gone + `" alt=""></figure>` +
		`<figure data-kapi-id="cccccccc"><img src="./plan.files/` + alsoGone + `" alt=""></figure>` +
		`<figure data-kapi-id="dddddddd"><img src="./plan.files/` + gone + `" alt=""></figure>`)
	for name, build := range map[string]func(string, []byte, string) ([]byte, error){"Document": Document, "Export": Export} {
		doc, err := build("plan.html", src, files)
		if err == nil {
			t.Errorf("%s left a relative image in the file:\n%s", name, doc)
			continue
		}
		want := "the page shows images that are missing from plan.files: " + gone + ", " + alsoGone
		if err.Error() != want || !errors.Is(err, ErrMissingImages) {
			t.Errorf("%s: %q, want %q", name, err, want)
		}
	}
}
