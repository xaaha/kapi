package daemon

import (
	"bufio"
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
	reopened := call(t, home, &Request{Op: "open", Page: path}).URL
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "after"})
	if id := firstEventID(t, reopened, "1"); id != "2" {
		t.Errorf("resumed stream starts at %s, want 2", id)
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
