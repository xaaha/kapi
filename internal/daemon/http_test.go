package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
)

func serving(t *testing.T) (home string, s *Server, stop func()) {
	t.Helper()
	home, err := os.MkdirTemp("", "kd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	s = New(home)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(HTTPURLPath(home)); err == nil {
			if conn, err := net.Dial("unix", SocketPath(home)); err == nil {
				conn.Close()
				return home, s, stop
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not come up")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func call(t *testing.T, home string, req *Request) Response {
	t.Helper()
	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func writePage(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDaemonServesPagesOverHTTPAndWritesTheURL(t *testing.T) {
	home, _, stop := serving(t)
	b, err := os.ReadFile(HTTPURLPath(home))
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(string(b), "\n")
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/[0-9a-f]{32}/$`).MatchString(base) {
		t.Fatalf("http.url = %q, want http://127.0.0.1:<port>/<token>/", b)
	}
	if fi, err := os.Stat(HTTPURLPath(home)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("http.url mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}

	path := writePage(t, "<p>hello</p>\n")
	resp := call(t, home, &Request{Op: "open", Page: path})
	if resp.Error != "" || resp.URL != base+"p/"+page.ID(path)+"/" {
		t.Fatalf("open = %+v", resp)
	}
	if src, _ := os.ReadFile(path); !strings.Contains(string(src), "data-kapi-id=") {
		t.Errorf("open did not give the page ids: %s", src)
	}
	got, err := http.Get(resp.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(got.Body)
	got.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusOK || !strings.Contains(string(body), `data-rev="1"`) || !strings.Contains(string(body), "hello") {
		t.Errorf("page: %s\n%s", got.Status, body)
	}

	if resp := call(t, home, &Request{Op: "open", Page: filepath.Join(home, "missing.html")}); resp.Error == "" {
		t.Error("open of a missing file succeeded")
	}

	stop()
	if _, err := os.Stat(HTTPURLPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("http.url left after shutdown: %v", err)
	}
}

func presence(l *eventlog.Log) []string {
	events, _ := l.After(0)
	var out []string
	for _, e := range events {
		if e.Kind == eventlog.KindPresence {
			out = append(out, e.Author+" "+e.Text)
		}
	}
	return out
}

func waitPresence(t *testing.T, l *eventlog.Log, want ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := presence(l)
		if strings.Join(got, ",") == strings.Join(want, ",") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("presence = %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWatchingReportsPresenceAndNeverDeliversIt(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "watch", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	waitPresence(t, l, "agent watching")

	call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "go"})
	var got Response
	if err := json.NewDecoder(conn).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Event == nil || got.Event.Kind != "message" || got.Event.Text != "go" {
		t.Fatalf("watch delivered %+v, want the message and no presence", got.Event)
	}
	if err := json.NewEncoder(conn).Encode(Request{Op: "ack", Seq: got.Event.Seq}); err != nil {
		t.Fatal(err)
	}
	waitPresence(t, l, "agent watching", "agent busy")

	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "done"})
	waitPresence(t, l, "agent watching", "agent busy", "agent watching")

	if err := json.NewDecoder(conn).Decode(&got); err != nil || got.Event.Text != "done" {
		t.Fatalf("watch delivered %+v, %v; want the agent's reply", got.Event, err)
	}
	conn.Close()
	waitPresence(t, l, "agent watching", "agent busy", "agent watching", "agent none")
	if p := call(t, home, &Request{Op: "pending", Page: path, Reader: "agent"}); len(p.Events) != 1 || p.Events[0].Text != "done" {
		t.Errorf("pending = %+v, want only the agent's own reply", p.Events)
	}
}

func TestARestartedDaemonClearsPresenceLeftBehind(t *testing.T) {
	home, err := os.MkdirTemp("", "kd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	path := writePage(t, "<p>x</p>")
	l, err := New(home).page(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []eventlog.Event{{Author: "agent", Text: "watching"}, {Author: "codex", Text: "busy"}, {Author: "codex", Text: "none"}} {
		e.Kind = eventlog.KindPresence
		if _, err := l.Append(&e); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	l, err = New(home).page(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(presence(l), ","); got != "agent watching,codex busy,codex none,agent none" {
		t.Errorf("presence after restart = %s", got)
	}
}
