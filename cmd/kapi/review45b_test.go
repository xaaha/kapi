package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
	if len(f.requests) != 1 {
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
