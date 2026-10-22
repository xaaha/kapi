package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/client"
	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
)

func TestReview45bHookContextFitsClaudeCodesTenThousandCharacterCap(t *testing.T) {
	long := strings.Repeat("x", 12000)
	inbox := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: []eventlog.Event{{Seq: 4, Author: "human", Kind: "message", Text: long}}}}}
	f := &fakeAPI{resp: inbox, stdin: `{"hook_event_name":"PostToolUse","cwd":"/w"}`}
	out, err := run(t, f, "hook-messages")
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		t.Fatal(err)
	}
	if n := len(reply.HookSpecificOutput.AdditionalContext); n > 10000 {
		t.Fatalf("additionalContext is %d chars; Claude Code replaces it with a 2,000 char preview, yet the inbox already marked #4 seen", n)
	}
	if !strings.Contains(reply.HookSpecificOutput.AdditionalContext, "kapi pending /w/plan.html") {
		t.Errorf("a cut message does not point to kapi pending:\n%s", reply.HookSpecificOutput.AdditionalContext[:300])
	}
	if slices.ContainsFunc(f.requests, func(r daemon.Request) bool { return r.Op == "seen" }) {
		t.Errorf("requests = %+v; a message the model only saw part of must stay unseen", f.requests)
	}
}

func TestHookMarksSeenOnlyThroughTheLastEventItShowedWhole(t *testing.T) {
	events := []eventlog.Event{
		{Seq: 2, Author: "human", Kind: "message", Text: "short"},
		{Seq: 3, Author: "human", Kind: "message", Text: strings.Repeat("y", 9500)},
		{Seq: 5, Author: "human", Kind: "message", Text: "after"},
	}
	f := &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: events}}}, stdin: `{"hook_event_name":"Stop","cwd":"/w"}`}
	out, err := run(t, f, "hook-messages")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#2 human message: short") || strings.Contains(out, "yyyy") || strings.Contains(out, "after") || !strings.Contains(out, "kapi pending /w/plan.html") {
		t.Errorf("output = %.400s", out)
	}
	if len(f.requests) != 3 || f.requests[2].Op != "seen" || f.requests[2].Seq != 2 {
		t.Errorf("requests = %+v, want seen through #2 only", f.requests)
	}
}

func TestAStopHookThatCanShowNothingWholeLetsTheAgentStop(t *testing.T) {
	inbox := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: []eventlog.Event{{Seq: 4, Author: "human", Kind: "message", Text: strings.Repeat("x", 12000)}}}}}
	f := &fakeAPI{resp: inbox, stdin: `{"hook_event_name":"Stop","cwd":"/w","stop_hook_active":true}`}
	if out, err := run(t, f, "hook-messages"); err != nil || out != "" {
		t.Errorf("Stop again on the same cut message = %.200q, %v; want it to let the agent stop instead of looping", out, err)
	}
}

func shortHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "kr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return home
}

func TestReview45bHookNeverStartsTheDaemon(t *testing.T) {
	home := shortHome(t)
	var out bytes.Buffer
	err := newRoot(client.Client{Home: home}, strings.NewReader(`{"hook_event_name":"PostToolUse","cwd":"/w"}`), &out, func(time.Duration) {}).Execute([]string{"hook-messages"})
	if err != nil || out.Len() != 0 {
		t.Fatalf("hook = %q, %v", out.String(), err)
	}
	if _, err := os.Stat(filepath.Join(home, "daemon.log")); err == nil {
		t.Fatal("the hook tried to start the daemon")
	}
}

func TestReview45bHookReturnsQuicklyWhenTheDaemonDoesNotAnswer(t *testing.T) {
	home := shortHome(t)
	ln, err := net.Listen("unix", daemon.SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 8)
	t.Cleanup(func() {
		for len(held) > 0 {
			(<-held).Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held <- c
		}
	}()
	done := make(chan struct{})
	var out bytes.Buffer
	var err2 error
	go func() {
		defer close(done)
		err2 = newRoot(client.Client{Home: home}, strings.NewReader(`{"hook_event_name":"PostToolUse","cwd":"/w"}`), &out, func(time.Duration) {}).Execute([]string{"hook-messages"})
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("hook-messages still blocked after 3s on a daemon that accepts but never answers; Claude Code waits its 10s timeout on every tool call")
	}
	if err2 != nil || out.Len() != 0 {
		t.Errorf("hook = %q, %v; want no output and a zero exit", out.String(), err2)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

func TestHookMarksNothingSeenWhenItsOutputIsLost(t *testing.T) {
	inbox := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: []eventlog.Event{{Seq: 4, Author: "human", Kind: "message", Text: "hi"}}}}}
	f := &fakeAPI{resp: inbox}
	if err := hookMessages(f, strings.NewReader(`{"hook_event_name":"PostToolUse","cwd":"/w"}`), failingWriter{}); err == nil {
		t.Error("a lost write went unreported")
	}
	if len(f.requests) != 1 {
		t.Errorf("requests = %+v; nothing may be marked seen when the model never got the text", f.requests)
	}
}
