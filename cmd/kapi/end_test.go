package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/client"
	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
)

func TestD25WatchExitsCleanlyOnAnEndInsteadOfReconnecting(t *testing.T) {
	end := daemon.Response{Event: &eventlog.Event{Seq: 7, Author: "human", Kind: eventlog.KindEnd}}
	f := &fakeAPI{watches: []func(fn func(daemon.Response) error) error{
		func(fn func(daemon.Response) error) error {
			if err := fn(end); err != nil {
				return err
			}
			return client.ErrConnectionLost
		},
		func(func(daemon.Response) error) error { t.Fatal("watch reconnected after the end"); return nil },
	}}
	out, err := run(t, f, "watch", "plan.html")
	if err != nil || len(f.watched) != 1 {
		t.Fatalf("watch: err %v, watched %v", err, f.watched)
	}
	if !strings.HasPrefix(out, "#7 human ended the session: stop working on this page and do not run kapi wait or kapi watch on it again") {
		t.Errorf("watch printed %q", out)
	}
}

func TestD25TheStopHookLetsTheAgentStopOnAnEndedPage(t *testing.T) {
	events := []eventlog.Event{{Seq: 3, Author: "human", Kind: "note", Text: "why?"}, {Seq: 4, Author: "human", Kind: eventlog.KindEnd}}
	f := &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Hooked: 2, Ended: true, Events: events}}}, stdin: `{"hook_event_name":"Stop","cwd":"/w"}`}
	if out, err := run(t, f, "hook-messages"); err != nil || out != "" || len(f.requests) != 1 {
		t.Fatalf("Stop on an ended page with an open note: out %q, err %v, requests %+v", out, err, f.requests)
	}
	f = &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Hooked: 2, Ended: true, Events: events}}}, stdin: `{"hook_event_name":"PostToolUse","cwd":"/w"}`}
	if out, _ := run(t, f, "hook-messages"); !strings.Contains(out, "#4 human ended the session") {
		t.Errorf("PostToolUse did not tell the agent the session ended: %s", out)
	}
}

func TestD25TheHomeViewLabelsAnEndedPage(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{
		{Path: "/w/a.html", Ended: true, Readers: []daemon.ReaderStatus{{Reader: "agent"}}},
		{Path: "/w/b.html", Readers: []daemon.ReaderStatus{{Reader: "agent"}}},
	}}}
	out, err := run(t, f)
	if err != nil || !strings.Contains(out, "  /w/a.html (ended): agent 0 unread\n") || !strings.Contains(out, "  /w/b.html: agent 0 unread\n") {
		t.Errorf("home view: %v\n%s", err, out)
	}
}

func (e *e2e) end(url string) int {
	e.t.Helper()
	req, err := http.NewRequest("POST", url+"end", strings.NewReader("{}"))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.URL.Host)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestD25EndingASessionStopsTheAgentAndANewMessageStartsItAgain(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	wait := func() string {
		cmd, out := e.start("", "wait", e.page)
		if !exited(cmd, 5*time.Second) || !cmd.ProcessState.Success() {
			t.Fatalf("kapi wait did not return: %v", cmd.ProcessState)
		}
		return out.String()
	}
	stop := func() string {
		input, err := json.Marshal(map[string]any{"hook_event_name": "Stop", "cwd": filepath.Dir(e.page)})
		if err != nil {
			t.Fatal(err)
		}
		cmd, out := e.start(string(input), "hook-messages")
		if !exited(cmd, 5*time.Second) || !cmd.ProcessState.Success() {
			t.Fatalf("Stop hook failed: %v", cmd.ProcessState)
		}
		return out.String()
	}

	w := e.watch()
	e.post(url, "first")
	if got := w.event(t); got != "human message: first" {
		t.Fatalf("watch printed %q", got)
	}
	if code := e.end(url); code != http.StatusOK {
		t.Fatalf("POST end: %d", code)
	}
	if got := w.event(t); !strings.HasPrefix(got, "human ended the session") {
		t.Fatalf("watch printed %q, want the end", got)
	}
	if !exited(w.cmd, 5*time.Second) || !w.cmd.ProcessState.Success() {
		t.Fatalf("watch did not exit cleanly after the end: %v", w.cmd.ProcessState)
	}
	if _, queued := receipts(t, url); queued != w.seq {
		t.Errorf("queued through %d, want the end #%d handed over", queued, w.seq)
	}
	if code := e.end(url); code != http.StatusConflict {
		t.Errorf("a second end: %d, want 409", code)
	}
	if got := e.kapi(); !strings.Contains(got, e.page+" (ended): ") {
		t.Errorf("home view does not mark the page ended:\n%s", got)
	}
	if got := stop(); got != "" {
		t.Errorf("Stop hook on an ended page = %q, want it to let the agent stop", got)
	}
	again := e.watch()
	if got := again.event(t); !strings.HasSuffix(got, "human message: first") {
		t.Fatalf("a new watch printed %q", got)
	}
	if got := again.event(t); !strings.HasPrefix(got, "human ended the session") {
		t.Fatalf("a new watch on an ended page printed %q, want the end", got)
	}
	if !exited(again.cmd, 5*time.Second) {
		t.Fatal("a new watch on an ended page kept running")
	}

	seq := e.post(url, "back again")
	if got := e.kapi(); strings.Contains(got, "(ended)") {
		t.Errorf("home view still marks the page ended after a message:\n%s", got)
	}
	e.post(url, "unread")
	if got := wait(); !strings.Contains(got, "human message: back again") || strings.Contains(got, "ended the session") {
		t.Errorf("kapi wait after a new message printed %q, want the message and no lifted end", got)
	}
	if got := stop(); !strings.Contains(got, `"decision":"block"`) || !strings.Contains(got, "human message: back again") || strings.Contains(got, "ended the session") {
		t.Errorf("Stop hook after a new message = %q, want it to hand the agent the message and no lifted end", got)
	}
	restarted := e.watch()
	e.post(url, "and more")
	if got := restarted.event(t); got != "human message: and more" || restarted.seq <= seq {
		t.Fatalf("a watch after the restart printed #%d %q", restarted.seq, got)
	}
	select {
	case line, ok := <-restarted.lines:
		t.Errorf("a watch after the restart printed %q (open %v), want it waiting", line, ok)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestD25AWatchOrWaitOnAnEndedPageWhoseEndWasSeenStillGetsTheEnd(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(e.kapi("open", "--print", e.page))
	w := e.watch()
	e.post(url, "first")
	w.event(t)
	if code := e.end(url); code != http.StatusOK {
		t.Fatalf("POST end: %d", code)
	}
	w.event(t)
	if !exited(w.cmd, 5*time.Second) {
		t.Fatal("watch kept running after the end")
	}
	e.kapi("seen", e.page, strconv.FormatUint(w.seq, 10))
	again := e.watch()
	if got := again.event(t); !strings.HasPrefix(got, "human ended the session") {
		t.Fatalf("a watch on an ended page whose end was seen printed %q, want the end", got)
	}
	if !exited(again.cmd, 5*time.Second) {
		t.Fatal("a watch on an ended page whose end was seen kept running")
	}
	cmd, out := e.start("", "wait", e.page)
	if !exited(cmd, 5*time.Second) || !strings.Contains(out.String(), "human ended the session") {
		t.Fatalf("kapi wait on an ended page whose end was seen printed %q, want the end at once", out.String())
	}
}
