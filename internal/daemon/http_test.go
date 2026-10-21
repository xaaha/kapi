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
	"sync"
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

func attachWatcher(t *testing.T, home, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := json.NewEncoder(conn).Encode(Request{Op: "watch", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestTwoWatchersOfOneReaderStayPresentUntilTheLastLeaves(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	first := attachWatcher(t, home, path)
	waitPresence(t, l, "agent watching")
	second := attachWatcher(t, home, path)
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	attached := func() int {
		s.presenceMu.Lock()
		defer s.presenceMu.Unlock()
		return s.watchers[path]["agent"]
	}
	waitFor("two watchers", func() bool { return attached() == 2 })

	first.Close()
	waitFor("one watcher", func() bool { return attached() == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := presence(l); len(got) != 1 {
		t.Fatalf("presence after the first watcher left = %v, want still watching", got)
	}
	st := call(t, home, &Request{Op: "status", Page: path})
	if len(st.Readers) != 1 || !st.Readers[0].Attached {
		t.Errorf("status = %+v, want agent attached", st.Readers)
	}

	second.Close()
	waitPresence(t, l, "agent watching", "agent none")
}

func TestConcurrentPresenceLandsInTheLogInOrder(t *testing.T) {
	home, err := os.MkdirTemp("", "kd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	s := New(home)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 20 {
				s.setPresence(path, "agent", []string{"watching", "busy"}[(i+j)%2], l)
			}
		}()
	}
	wg.Wait()
	got := presence(l)
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Fatalf("presence %d and %d are both %q: updates landed out of order", i-1, i, got[i])
		}
	}
	if want := "agent " + s.presenceOf(path, "agent"); got[len(got)-1] != want {
		t.Errorf("last presence in the log = %q, daemon thinks %q", got[len(got)-1], want)
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

func TestOpenReportsALossyConversion(t *testing.T) {
	home, _, stop := serving(t)
	defer stop()
	raw := "<style>p{}</style><p onclick=\"x()\">hi</p><script>x()</script>\n"
	path := writePage(t, raw)
	resp := call(t, home, &Request{Op: "open", Page: path})
	want := "converted to kapi format: removed 1 script, 1 style, 1 attribute; original saved"
	if resp.Error != "" || resp.Notice != want {
		t.Fatalf("open = %+v, want notice %q", resp, want)
	}
	if orig, err := os.ReadFile(resp.Original); err != nil || string(orig) != raw {
		t.Errorf("original %q = %q, %v", resp.Original, orig, err)
	}
	pending := call(t, home, &Request{Op: "pending", Page: path, Reader: "agent"})
	found := false
	for _, e := range pending.Events {
		found = found || (e.Kind == "revision" && e.Text == want)
	}
	if !found {
		t.Errorf("no revision event with the notice in %+v", pending.Events)
	}
	if again := call(t, home, &Request{Op: "open", Page: path}); again.Notice != "" || again.Original != "" {
		t.Errorf("second open repeats the notice: %+v", again)
	}

	clean := writePage(t, "<p>clean</p>\n")
	if resp := call(t, home, &Request{Op: "open", Page: clean}); resp.Notice != "" || resp.Original != "" {
		t.Errorf("clean page open = %+v", resp)
	}
}
