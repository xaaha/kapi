package main

import (
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
)

func TestReview45cATruncatedEventIsHandedOverSoTheAgentCanNameItAndItIsNotReinjected(t *testing.T) {
	inbox := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: []eventlog.Event{{Seq: 4, Author: "human", Kind: "message", Text: strings.Repeat("x", 12000)}}}}}
	f := &fakeAPI{resp: inbox, stdin: `{"hook_event_name":"PostToolUse","cwd":"/w"}`}
	out, err := run(t, f, "hook-messages")
	if err != nil || !strings.Contains(out, "#4 human message: xxx") {
		t.Fatalf("hook = %q, %v", out, err)
	}
	for _, r := range f.requests[1:] {
		if r.Op == "queue" && r.Seq == 4 {
			return
		}
	}
	t.Fatalf("requests after the hook showed #4 cut = %+v; #4 stays unqueued, so `kapi seen plan.html 4` and `kapi say --seen 4` are refused as never handed over, and every later PostToolUse injects the same 9,000 characters again", f.requests[1:])
}

func TestACutEventShowsAgainOnlyAtAPromptOrAStopAndStaysUnseen(t *testing.T) {
	cutOnce := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Queued: 4, Events: []eventlog.Event{{Seq: 4, Author: "human", Kind: "message", Text: strings.Repeat("x", 12000)}}}}}
	f := &fakeAPI{resp: cutOnce, stdin: `{"hook_event_name":"PostToolUse","cwd":"/w"}`}
	if out, err := run(t, f, "hook-messages"); err != nil || out != "" || len(f.requests) != 1 {
		t.Errorf("PostToolUse after the cut was shown = %.100q, %v, %+v; want nothing", out, err, f.requests)
	}
	for _, event := range []string{"UserPromptSubmit", "Stop"} {
		f := &fakeAPI{resp: cutOnce, stdin: `{"hook_event_name":"` + event + `","cwd":"/w"}`}
		out, err := run(t, f, "hook-messages")
		if err != nil || !strings.Contains(out, "#4 human message: xxx") || !strings.Contains(out, "kapi pending /w/plan.html") {
			t.Errorf("%s = %.200q, %v; want the cut message and the pointer again", event, out, err)
		}
		for _, r := range f.requests {
			if r.Op == "seen" {
				t.Errorf("%s marked %+v seen; the model saw only part of #4", event, r)
			}
		}
	}
}
