package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	return e.watchAs("agent")
}

func (e *e2e) watchAs(reader string) *watcher {
	e.t.Helper()
	cmd := exec.Command(e.bin, "watch", "--reader", reader, e.page) //nolint:gosec // runs the kapi binary this test just built
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
	if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(page), `<main id="kapi-page" data-rev="1" data-undo="false" data-path=`) {
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
	e.seen(w.seq)

	w.kill9(t)
	e.kapi("send", e.page, "sent while the watcher is dead")
	waitFor(t, "status to show the watcher gone", func() bool {
		return strings.Contains(e.kapi("status", e.page), "agent: not attached, 1 unread")
	})

	w2 := e.watch()
	if got := w2.event(t); got != "human message: sent while the watcher is dead" {
		t.Fatalf("after restart, first event = %q, want only the message sent while it was dead", got)
	}
	e.seen(w2.seq)

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

func TestWatchDoesNotEchoTheAgentsOwnMessages(t *testing.T) {
	e := newE2E(t)
	w := e.watch()
	waitFor(t, "the watcher to attach", func() bool { return strings.Contains(e.kapi("status", e.page), "agent: attached") })
	e.kapi("say", e.page, "my own reply")
	e.kapi("send", e.page, "the human answers")
	if got := w.event(t); got != "human message: the human answers" {
		t.Fatalf("watch printed %q, want the human's message and not the agent's own reply", got)
	}
	waitFor(t, "the ack", func() bool { return e.kapi("pending", e.page) == "" })
	if got := e.kapi(); !strings.Contains(got, "agent 0 unread (watching)") {
		t.Errorf("home view = %q, want the agent's own reply left out of its unread count", got)
	}
	if got := e.kapi("pending", "--reader", "codex", e.page); !strings.Contains(got, "agent message: my own reply") || !strings.Contains(got, "human message: the human answers") {
		t.Errorf("codex pending = %q, want the agent's reply and the human's message", got)
	}
}

func TestTwoAgentsWithDifferentReadersSeeEachOther(t *testing.T) {
	e := newE2E(t)
	agent, codex := e.watchAs("agent"), e.watchAs("codex")
	waitFor(t, "both watchers to attach", func() bool {
		st := e.kapi("status", e.page)
		return strings.Contains(st, "agent: attached") && strings.Contains(st, "codex: attached")
	})
	e.kapi("say", "--reader", "codex", e.page, "codex here")
	e.kapi("say", e.page, "agent here")
	if got := agent.event(t); got != "codex message: codex here" {
		t.Errorf("agent saw %q, want codex's message and not its own", got)
	}
	if got := codex.event(t); got != "agent message: agent here" {
		t.Errorf("codex saw %q, want the agent's message and not its own", got)
	}
}

func TestReservedReaderNamesAreRefused(t *testing.T) {
	e := newE2E(t)
	e.kapi("send", e.page, "hello")
	for _, args := range [][]string{
		{"watch", "--reader", "human", e.page},
		{"pending", "--reader", "kapi", e.page},
		{"say", "--reader", "human", e.page, "pretending"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, e.bin, args...) //nolint:gosec // runs the kapi binary this test just built
		cmd.Env = e.env
		out, err := cmd.CombinedOutput()
		cancel()
		if err == nil || !strings.Contains(string(out), "is reserved") {
			t.Errorf("kapi %v: %v\n%s; want a reserved reader error", args, err, out)
		}
	}
	if got := e.kapi("pending", "--reader", "codex", e.page); !strings.Contains(got, "human message: hello") {
		t.Errorf("pending = %q", got)
	}
}

func TestTheAgentsWatchSeesFileEdits(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<p data-kapi-id=\"aaaaaaaa\">a</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	resp, err := http.Get(url) //nolint:gosec // the local URL kapi just printed
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	w := e.watch()
	waitFor(t, "the watcher to attach", func() bool { return strings.Contains(e.kapi("status", e.page), "agent: attached") })
	if err := os.WriteFile(e.page, []byte("<p data-kapi-id=\"aaaaaaaa\">edited by hand</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := w.event(t); got != `file revision on #aaaaaaaa: edited in the file: "edited by hand"` {
		t.Errorf("watch printed %q, want the file edit", got)
	}
	for _, args := range [][]string{{"pending", "--reader", "file", e.page}} {
		cmd := exec.Command(e.bin, args...) //nolint:gosec // runs the kapi binary this test just built
		cmd.Env = e.env
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "is reserved") {
			t.Errorf("kapi %v: %v\n%s; want a reserved reader error", args, err, out)
		}
	}
}

func (e *e2e) seen(seq uint64) {
	e.t.Helper()
	waitFor(e.t, "watch to queue the line so kapi seen accepts it", func() bool {
		cmd := exec.Command(e.bin, "seen", e.page, strconv.FormatUint(seq, 10)) //nolint:gosec // runs the kapi binary this test just built
		cmd.Env = e.env
		out, err := cmd.Output()
		return err == nil && string(out) == fmt.Sprintf("seen through #%d\n", seq)
	})
}

func (e *e2e) post(url, text string) uint64 {
	e.t.Helper()
	body, err := json.Marshal(map[string]string{"kind": "message", "text": text})
	if err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequest("POST", url+"messages", bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.URL.Host)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Seq uint64 }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		e.t.Fatalf("POST messages: %s %v", resp.Status, err)
	}
	return out.Seq
}

func receipts(t *testing.T, url string) (read, queued uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	stop := time.AfterFunc(300*time.Millisecond, cancel)
	defer stop.Stop()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var r struct {
			Kind, Reader string
			Seq          uint64
		}
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok || json.Unmarshal([]byte(data), &r) != nil || r.Reader != "agent" {
			continue
		}
		switch r.Kind {
		case "read":
			read = r.Seq
		case "queued":
			queued = r.Seq
		}
	}
	return read, queued
}

func TestAPrintedMessageIsQueuedAndSeenOnlyWhenTheAgentActs(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	w := e.watch()
	waitFor(t, "the watcher to attach", func() bool { return strings.Contains(e.kapi("status", e.page), "agent: attached") })
	seq := e.post(url, "are you there?")
	if got := w.event(t); got != "human message: are you there?" {
		t.Fatalf("watch printed %q", got)
	}
	waitFor(t, "the message to be queued", func() bool { _, q := receipts(t, url); return q >= seq })
	if read, _ := receipts(t, url); read >= seq {
		t.Fatalf("read position %d covers #%d although the agent only had it printed", read, seq)
	}

	w.kill9(t)
	again := e.watch()
	if got := again.event(t); got != "human message: are you there?" {
		t.Fatalf("a new watch printed %q, want the unseen message again", got)
	}

	e.kapi("say", "--seen", strconv.FormatUint(seq, 10), e.page, "yes")
	waitFor(t, "the reply to mark it seen", func() bool { read, _ := receipts(t, url); return read >= seq })
}

func (e *e2e) start(stdin string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...) //nolint:gosec // runs the kapi binary this test just built
	cmd.Env = e.env
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, &out
}

func exited(cmd *exec.Cmd, within time.Duration) bool {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

func TestWaitExitsWithTheMessageWithinSecondsAndTheReplyMarksItSeen(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	wait, out := e.start("", "wait", e.page)
	waitFor(t, "the wait to attach", func() bool { return strings.Contains(e.kapi("status", e.page), "agent: attached") })
	sent := time.Now()
	seq := e.post(url, "are you there?")
	if !exited(wait, 3*time.Second) {
		t.Fatal("kapi wait did not exit within 3s of the message")
	}
	if !strings.HasSuffix(out.String(), " human message: are you there?\n") {
		t.Fatalf("wait printed %q", out.String())
	}
	t.Logf("message reached the agent in %v", time.Since(sent))
	if read, queued := receipts(t, url); queued < seq || read >= seq {
		t.Fatalf("read %d, queued %d; want #%d queued and not yet seen", read, queued, seq)
	}

	again, _ := e.start("", "wait", e.page)
	if exited(again, 500*time.Millisecond) {
		t.Fatal("a second kapi wait exited at once with nothing new")
	}
	e.kapi("say", "--seen", strconv.FormatUint(seq, 10), e.page, "yes")
	waitFor(t, "the reply to mark it seen", func() bool { read, _ := receipts(t, url); return read >= seq })
}

func TestAMessageBehindACutOneReachesTheModelAndStopDoesNotBlockTwice(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	hook := func(event string) string {
		cmd, out := e.start(`{"hook_event_name":"`+event+`","cwd":"`+filepath.Dir(e.page)+`"}`, "hook-messages")
		if !exited(cmd, 5*time.Second) || !cmd.ProcessState.Success() {
			t.Fatalf("%s hook failed: %v", event, cmd.ProcessState)
		}
		return out.String()
	}
	long := e.post(url, strings.Repeat("x", 12000))
	if got := hook("PostToolUse"); !strings.Contains(got, fmt.Sprintf("to read #%d", long)) || len(got) > 2000 {
		t.Fatalf("PostToolUse = %.300q, want a short placeholder for #%d", got, long)
	}
	urgent := e.post(url, "URGENT: stop the deploy")
	if got := hook("PostToolUse"); !strings.Contains(got, "URGENT: stop the deploy") || strings.Contains(got, "xxxx") {
		t.Fatalf("PostToolUse = %.300q, want only the new message", got)
	}
	if got := hook("Stop"); got != "" {
		t.Errorf("Stop with nothing new = %.200q, want it to let the agent stop", got)
	}
	read, queued := receipts(t, url)
	if queued < urgent || read >= long {
		t.Errorf("read %d, queued %d; want both handed over and the cut #%d still unseen", read, queued, long)
	}
	e.kapi("seen", e.page, strconv.FormatUint(urgent, 10))
	if read, _ := receipts(t, url); read < urgent {
		t.Errorf("read %d after kapi seen %d", read, urgent)
	}
}

func TestAHookDoesNotStartTheDaemon(t *testing.T) {
	e := newE2E(t)
	cmd, out := e.start(`{"hook_event_name":"Stop","cwd":"`+filepath.Dir(e.page)+`"}`, "hook-messages")
	if !exited(cmd, 5*time.Second) || !cmd.ProcessState.Success() || out.Len() != 0 {
		t.Fatalf("hook with no daemon: %v, output %q; want a quiet zero exit", cmd.ProcessState, out.String())
	}
	if _, err := os.Stat(filepath.Join(e.home, "kapi.sock")); !os.IsNotExist(err) {
		t.Errorf("socket after the hook: %v; a hook in any session must not start the daemon and reopen tabs", err)
	}
}

func TestTheStopHookHoldsTheAgentUntilItHasSeenTheMessage(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	hook := func(event string, active bool) string {
		input, err := json.Marshal(map[string]any{"hook_event_name": event, "cwd": filepath.Dir(e.page), "stop_hook_active": active})
		if err != nil {
			t.Fatal(err)
		}
		cmd, out := e.start(string(input), "hook-messages")
		if !exited(cmd, 5*time.Second) || !cmd.ProcessState.Success() {
			t.Fatalf("%s hook failed: %v", event, cmd.ProcessState)
		}
		return out.String()
	}
	w := e.watch()
	seq := e.post(url, "stop and look")
	if got := w.event(t); got != "human message: stop and look" {
		t.Fatalf("watch printed %q", got)
	}
	waitFor(t, "the watch to queue it", func() bool { _, q := receipts(t, url); return q >= seq })
	e.kapi("say", e.page, "about something else")
	if read, _ := receipts(t, url); read >= seq {
		t.Fatalf("read %d covers #%d after a reply that named no event", read, seq)
	}
	if got := hook("PostToolUse", false); !strings.Contains(got, `"additionalContext"`) || !strings.Contains(got, "human message: stop and look") {
		t.Fatalf("PostToolUse hook = %q, want the watch-printed message as context", got)
	}
	if read, _ := receipts(t, url); read < seq {
		t.Errorf("read %d, want #%d seen once the hook put it in the model's context", read, seq)
	}
	if got := hook("PostToolUse", false) + hook("Stop", false); got != "" {
		t.Errorf("hooks after delivery = %q, want nothing new", got)
	}

	next := e.post(url, "one more")
	if got := hook("Stop", false); !strings.Contains(got, `"decision":"block"`) || !strings.Contains(got, "human message: one more") || strings.Contains(got, "stop and look") {
		t.Fatalf("Stop hook = %q, want it to block on the new message only", got)
	}
	if got := hook("Stop", true); got != "" {
		t.Errorf("Stop hook while already continuing = %q, want it to let the agent stop", got)
	}
	if read, _ := receipts(t, url); read < next {
		t.Errorf("read %d, want #%d seen after the Stop hook showed it", read, next)
	}
}

func TestAttachAddsAnImageBlockAsTheAgent(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1 data-kapi-id=\"aaaaaaaa\">Plan</h1>\n<p data-kapi-id=\"bbbbbbbb\">Text</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	img := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(img, png, 0o600); err != nil {
		t.Fatal(err)
	}
	got := e.kapi("attach", "--after", "aaaaaaaa", e.page, img)
	m := regexp.MustCompile(`^added #([a-z2-7]{8}) \(added an image after .*\)\n$`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("attach printed %q", got)
	}
	src, err := os.ReadFile(e.page)
	if err != nil {
		t.Fatal(err)
	}
	fig := regexp.MustCompile(`</h1>\n<figure data-kapi-id="` + m[1] + `"><img src="\./plan\.files/([0-9a-f]{32}\.png)" alt=""></figure>\n<p`).FindSubmatch(src)
	if fig == nil {
		t.Fatalf("page source:\n%s", src)
	}
	if stored, err := os.ReadFile(filepath.Join(filepath.Dir(e.page), "plan.files", string(fig[1]))); err != nil || !bytes.Equal(stored, png) {
		t.Errorf("stored image: %v", err)
	}
	if pending := e.kapi("pending", "--reader", "codex", e.page); !strings.Contains(pending, " agent revision on #"+m[1]+": added an image after ") {
		t.Errorf("another reader sees %q", pending)
	}
	bad := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(bad, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e.bin, "attach", e.page, bad) //nolint:gosec // runs the kapi binary this test just built
	cmd.Env = e.env
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "only PNG, JPEG, GIF and WebP") {
		t.Errorf("attaching a text file: %v %s", err, out)
	}
}

func TestAttachLeavesNothingBehindWhenItFails(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<p data-kapi-id=\"aaaaaaaa\">Text</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	img := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(img, png, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(e.page)
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("# notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"attach", filepath.Join(dir, "typo.html"), img}, "no such file"},
		{[]string{"attach", "--after", "zzzzzzzz", e.page, img}, "no such element"},
		{[]string{"attach", notes, img}, "not an HTML page"},
		{[]string{"attach", "--reader", "", e.page, img}, "cannot be empty"},
		{[]string{"attach", "--reader", "Human", e.page, img}, "reserved"},
	} {
		cmd := exec.Command(e.bin, c.args...) //nolint:gosec // runs the kapi binary this test just built
		cmd.Env = e.env
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), c.want) {
			t.Errorf("kapi %v: %v %s, want %q", c.args, err, out, c.want)
		}
	}
	for _, left := range []string{"typo.files", "plan.files", "notes.files"} {
		if _, err := os.Stat(filepath.Join(dir, left)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was left behind: %v", left, err)
		}
	}
	if pages, _ := os.ReadDir(filepath.Join(e.home, "pages")); len(pages) > 1 {
		t.Errorf("a missing page was registered: %d pages", len(pages))
	}
}
