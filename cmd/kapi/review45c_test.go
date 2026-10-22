package main

import (
	"reflect"
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
		if r.Op == "handover" && r.Seq == 4 {
			return
		}
	}
	t.Fatalf("requests after the hook showed #4 cut = %+v; #4 stays unqueued, so `kapi seen plan.html 4` and `kapi say --seen 4` are refused as never handed over, and every later PostToolUse injects the same 9,000 characters again", f.requests[1:])
}

func TestEveryHookShowsANewCutEventOnceHandsItOverAndLeavesItUnseen(t *testing.T) {
	cut := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: []eventlog.Event{{Seq: 4, Author: "human", Kind: "message", Text: strings.Repeat("x", 12000)}}}}}
	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "Stop"} {
		f := &fakeAPI{resp: cut, stdin: `{"hook_event_name":"` + event + `","cwd":"/w"}`}
		out, err := run(t, f, "hook-messages")
		if err != nil || !strings.Contains(out, "#4 human message: xxx") || !strings.Contains(out, "kapi pending /w/plan.html` to read #4") || len(out) > 1000 {
			t.Errorf("%s = %.200q, %v; want a short placeholder for #4 and the pointer", event, out, err)
		}
		want := []daemon.Request{
			{Op: "inbox", Page: "/w", Reader: "agent"},
			{Op: "handover", Page: "/w/plan.html", Reader: "agent", Seq: 4, Cut: []uint64{4}},
		}
		if !reflect.DeepEqual(f.requests, want) {
			t.Errorf("%s requests = %+v, want #4 handed over and not seen", event, f.requests)
		}
	}
}
