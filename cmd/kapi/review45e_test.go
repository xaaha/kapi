package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
)

type seenFails struct{ *fakeAPI }

func (f seenFails) Do(req *daemon.Request) (daemon.Response, error) {
	r, _ := f.fakeAPI.Do(req)
	if req.Op == "seen" || req.Op == "handover" {
		return daemon.Response{}, errors.New("i/o timeout")
	}
	return r, nil
}

func TestReview45eAFailedSeenCallDoesNotLeaveAWholeEventLookingCut(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: []eventlog.Event{{Seq: 1, Author: "human", Kind: "message", Text: "short"}}}}}}
	if err := hookMessages(seenFails{f}, strings.NewReader(`{"hook_event_name":"PostToolUse","cwd":"/w"}`), &strings.Builder{}); err == nil {
		t.Fatal("want the seen error")
	}
	for _, r := range f.requests {
		if r.Op != "inbox" && r.Op != "handover" {
			t.Fatalf("requests = %+v: hooked #1 is stored though seen failed, so the next inbox has hooked 1 > seen 0, reads #1 as a cut event, and no later event on the page is ever marked seen by a hook", f.requests)
		}
	}
}
