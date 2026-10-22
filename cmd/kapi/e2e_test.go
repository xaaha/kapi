package main

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type e2e struct {
	t      *testing.T
	bin    string
	home   string
	env    []string
	page   string
	opened string
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the kapi binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "kapi")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	home, err := os.MkdirTemp("", "kapi")
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !slices.Contains([]string{"KAPI_NO_BROWSER", "SSH_CONNECTION", "SSH_TTY", "CI"}, name) {
			env = append(env, kv)
		}
	}
	e := &e2e{t: t, bin: bin, home: home, env: append(env, "KAPI_HOME="+home), page: filepath.Join(dir, "plan.html")}
	e.fakeOpeners(dir)
	t.Cleanup(func() {
		e.killDaemon()
		os.RemoveAll(home)
	})
	return e
}

func (e *e2e) fakeOpeners(dir string) {
	e.t.Helper()
	bin := filepath.Join(dir, "openers")
	if err := os.Mkdir(bin, 0o700); err != nil {
		e.t.Fatal(err)
	}
	e.opened = filepath.Join(dir, "opened")
	script := "#!/bin/sh\necho \"$1\" >> '" + e.opened + "'\n"
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil { //nolint:gosec // a test opener must be executable
			e.t.Fatal(err)
		}
	}
	e.env = append(e.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (e *e2e) kapi(args ...string) string {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...) //nolint:gosec // runs the kapi binary this test just built
	cmd.Env = e.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("kapi %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (e *e2e) killDaemon() {
	if out, err := exec.Command("pkill", "-f", e.bin+" daemon").CombinedOutput(); err != nil && len(out) > 0 { //nolint:gosec // matches only the kapi binary this test just built
		e.t.Logf("pkill: %v %s", err, out)
	}
	waitFor(e.t, "the daemon to exit", func() bool {
		return exec.Command("pgrep", "-f", e.bin+" daemon").Run() != nil //nolint:gosec // matches only the kapi binary this test just built
	})
}

type watcher struct {
	cmd   *exec.Cmd
	lines chan string
	seq   uint64
}

func (e *e2e) watch() *watcher {
	e.t.Helper()
	cmd := exec.Command(e.bin, "watch", e.page) //nolint:gosec // runs the kapi binary this test just built
	cmd.Env = e.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	w := &watcher{cmd: cmd, lines: make(chan string, 16)}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			w.lines <- sc.Text()
		}
		close(w.lines)
	}()
	e.t.Cleanup(func() {
		if err := cmd.Process.Kill(); err == nil {
			_ = cmd.Wait()
		}
	})
	return w
}

func (w *watcher) next(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-w.lines:
		if !ok {
			t.Fatal("watch exited")
		}
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
	}
	return ""
}

// event returns the next line without its event number, which must be higher than the last one.
func (w *watcher) event(t *testing.T) string {
	t.Helper()
	line := w.next(t)
	num, rest, ok := strings.Cut(line, " ")
	seq, err := strconv.ParseUint(strings.TrimPrefix(num, "#"), 10, 64)
	if !ok || err != nil || !strings.HasPrefix(num, "#") {
		t.Fatalf("line %q does not start with #<seq>", line)
	}
	if seq <= w.seq {
		t.Fatalf("event #%d after #%d: replayed or out of order", seq, w.seq)
	}
	w.seq = seq
	return rest
}

func (w *watcher) kill9(t *testing.T) {
	t.Helper()
	if err := w.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = w.cmd.Wait()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestOpenServesThePageAndBrowserMessagesReachTheAgent(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.Contains(url, "/p/") || strings.Contains(url, "?") {
		t.Fatalf("open printed %q", url)
	}
	browser := &http.Client{}
	resp, err := browser.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(page), `<main id="kapi-page" data-rev="1">`) {
		t.Fatalf("GET %s: %s %v\n%s", url, resp.Status, err, page)
	}

	post, err := http.NewRequest("POST", url+"messages", strings.NewReader(`{"kind":"message","text":"from the browser"}`))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("Origin", "http://"+post.URL.Host)
	resp, err = browser.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST messages: %s", resp.Status)
	}
	if got := e.kapi("pending", e.page); !strings.HasSuffix(got, " human message: from the browser\n") {
		t.Errorf("pending = %q", got)
	}
}

func TestD10EndToEndChannelLosesNothing(t *testing.T) {
	e := newE2E(t)

	e.kapi("send", e.page, "queued before anyone listens")
	e.kapi("send", "--on", "cutover", e.page, "second note")

	w := e.watch()
	if got := w.event(t); got != "human message: queued before anyone listens" {
		t.Fatalf("first event = %q", got)
	}
	if got := w.event(t); got != "human message on #cutover: second note" {
		t.Fatalf("second event = %q", got)
	}
	waitFor(t, "acks for #1 and #2", func() bool { return e.kapi("pending", e.page) == "" })

	w.kill9(t)
	e.kapi("send", e.page, "sent while the watcher is dead")
	waitFor(t, "status to show the watcher gone", func() bool {
		return strings.Contains(e.kapi("status", e.page), "agent: not attached, 1 unread")
	})

	w2 := e.watch()
	if got := w2.event(t); got != "human message: sent while the watcher is dead" {
		t.Fatalf("after restart, first event = %q, want only the message sent while it was dead", got)
	}

	e.killDaemon()
	e.kapi("send", e.page, "after the daemon restarted")
	if got := w2.event(t); got != "human message: after the daemon restarted" {
		t.Fatalf("after daemon restart, event = %q", got)
	}

	e.kapi("send", e.page, "fifth")
	e.kapi("send", e.page, "sixth")
	for _, want := range []string{"human message: fifth", "human message: sixth"} {
		if got := w2.event(t); got != want {
			t.Fatalf("live event = %q, want %q (each event exactly once)", got, want)
		}
	}
}
