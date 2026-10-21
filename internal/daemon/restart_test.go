package daemon

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func shortHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "kd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return home
}

func httpURL(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(HTTPURLPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}

func firstEventID(t *testing.T, pageURL, lastID string) string {
	t.Helper()
	r, err := http.NewRequest("GET", pageURL+"events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastID != "" {
		r.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("events after restart: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events: %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if id, ok := strings.CutPrefix(sc.Text(), "id: "); ok {
			return id
		}
	}
	t.Fatalf("stream ended without an event: %v", sc.Err())
	return ""
}

func TestARestartedDaemonKeepsThePortWithANewTokenAndTheStreamResumes(t *testing.T) {
	home := shortHome(t)
	path := writePage(t, "<p>x</p>\n")
	_, stop := servingAt(t, home)
	before, err := url.Parse(httpURL(t, home))
	if err != nil {
		t.Fatal(err)
	}
	pageURL := call(t, home, &Request{Op: "open", Page: path}).URL
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "before"})
	if id := firstEventID(t, pageURL, ""); id != "1" {
		t.Fatalf("first event id = %s", id)
	}
	stop()

	_, stop = servingAt(t, home)
	defer stop()
	after, err := url.Parse(httpURL(t, home))
	if err != nil {
		t.Fatal(err)
	}
	if after.Host != before.Host || after.Path == before.Path {
		t.Fatalf("http.url after restart = %s, was %s; want the same port and a new token", after, before)
	}
	if fi, err := os.Stat(HTTPURLPath(home)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("http.url mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	resp, err := http.Get(pageURL + "events") //nolint:gosec // the daemon under test gave this URL
	if err != nil {
		t.Fatal(err)
	}
	notice, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"error":"kapi restarted","reopen":"kapi open ` + path + `"}` + "\n"
	if resp.StatusCode != http.StatusForbidden || string(notice) != want {
		t.Errorf("old tab's events after restart: %s %q, want 403 %q", resp.Status, notice, want)
	}
	reopened := call(t, home, &Request{Op: "open", Page: path}).URL
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "after"})
	if id := firstEventID(t, reopened, "1"); id != "2" {
		t.Errorf("resumed stream starts at %s, want 2", id)
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestOnlyAWellFormedHTTPURLHasItsPortReused(t *testing.T) {
	const tok = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name  string
		saved func(port string) string
		reuse bool
	}{
		{"well formed", func(p string) string { return "http://127.0.0.1:" + p + "/" + tok + "/\n" }, true},
		{"all interfaces", func(p string) string { return "http://0.0.0.0:" + p + "/" + tok + "/\n" }, false},
		{"ipv6 loopback", func(p string) string { return "http://[::1]:" + p + "/" + tok + "/\n" }, false},
		{"localhost", func(p string) string { return "http://localhost:" + p + "/" + tok + "/\n" }, false},
		{"short token", func(p string) string { return "http://127.0.0.1:" + p + "/0123abcd/\n" }, false},
		{"non-hex token", func(p string) string { return "http://127.0.0.1:" + p + "/" + strings.Repeat("z", 32) + "/\n" }, false},
		{"junk after the url", func(p string) string { return "http://127.0.0.1:" + p + "/" + tok + "/x\n" }, false},
		{"junk after the newline", func(p string) string { return "http://127.0.0.1:" + p + "/" + tok + "/\njunk" }, false},
		{"junk before the url", func(p string) string { return "x http://127.0.0.1:" + p + "/" + tok + "/\n" }, false},
		{"https", func(p string) string { return "https://127.0.0.1:" + p + "/" + tok + "/\n" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := shortHome(t)
			port := freePort(t)
			if err := os.WriteFile(HTTPURLPath(home), []byte(tc.saved(port)), 0o600); err != nil {
				t.Fatal(err)
			}
			_, stop := servingAt(t, home)
			defer stop()
			got, err := url.Parse(httpURL(t, home))
			if err != nil {
				t.Fatal(err)
			}
			if got.Hostname() != "127.0.0.1" || strings.Contains(got.Path, tok) {
				t.Fatalf("http.url = %s, want 127.0.0.1 and a fresh token", got)
			}
			if reused := got.Port() == port; reused != tc.reuse {
				t.Errorf("from %q: http.url = %s, port reused = %v, want %v", tc.saved(port), got, reused, tc.reuse)
			}
		})
	}
}

func TestATakenPortGetsANewPortAndANewToken(t *testing.T) {
	home := shortHome(t)
	_, stop := servingAt(t, home)
	before, err := url.Parse(httpURL(t, home))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	squatter, err := net.Listen("tcp", before.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.Close()

	_, stop = servingAt(t, home)
	defer stop()
	after, err := url.Parse(httpURL(t, home))
	if err != nil {
		t.Fatal(err)
	}
	if after.Host == before.Host || after.Path == before.Path {
		t.Fatalf("http.url = %s after %s was taken, want a new port and a new token", after, before)
	}
	resp, err := http.Get(after.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: %s", after, resp.Status)
	}
}
