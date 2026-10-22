package main

import (
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
)

func cutThenShort() daemon.Response {
	return daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Queued: 1, Hooked: 1, Events: []eventlog.Event{
		{Seq: 1, Author: "human", Kind: "message", Text: strings.Repeat("x", 12000)},
		{Seq: 2, Author: "human", Kind: "message", Text: "URGENT: stop the deploy"},
	}}}}
}

func TestReview45dANewMessageBehindAQueuedCutEventReachesTheModel(t *testing.T) {
	for _, event := range []string{"PostToolUse", "Stop"} {
		f := &fakeAPI{resp: cutThenShort(), stdin: `{"hook_event_name":"` + event + `","cwd":"/w"}`}
		out, err := run(t, f, "hook-messages")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "URGENT: stop the deploy") {
			t.Errorf("%s output = %.120q...; #2 is never shown while the cut #1 is unseen", event, out)
		}
	}
}

func TestReview45dStopDoesNotBlockEveryTurnOnTheSameQueuedCutEvent(t *testing.T) {
	resp := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Queued: 1, Hooked: 1, Events: []eventlog.Event{
		{Seq: 1, Author: "human", Kind: "message", Text: strings.Repeat("x", 12000)},
	}}}}
	f := &fakeAPI{resp: resp, stdin: `{"hook_event_name":"Stop","cwd":"/w","stop_hook_active":false}`}
	out, err := run(t, f, "hook-messages")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"decision":"block"`) {
		t.Errorf("Stop blocks again on #1, already queued and shown cut in an earlier turn; the contract says a later Stop blocks only on events it can show whole and has not shown")
	}
}
