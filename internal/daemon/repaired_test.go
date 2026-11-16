package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
)

func TestARepairedEventReachesTheAgentThroughWaitAndTheInbox(t *testing.T) {
	home, s, _ := serving(t)
	pth := writePage(t, "<p>x</p>")
	l, err := s.page(pth)
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan Response, 1)
	go func() { replies <- call(t, home, &Request{Op: "wait", Page: pth, Reader: "agent"}) }()
	time.Sleep(100 * time.Millisecond)
	e, err := l.Append(&eventlog.Event{Author: "kapi", Kind: eventlog.KindRepaired, Text: "<kapi-kv> around a <dl>: rendered the <dl>"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-replies:
		if len(r.Events) != 1 || r.Events[0].Seq != e.Seq {
			t.Fatalf("wait = %+v, want the repaired event", r.Events)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not wake for a repaired event")
	}
	got := call(t, home, &Request{Op: "inbox", Page: filepath.Dir(pth), Reader: "agent"})
	if len(got.Pages) != 1 || got.Pages[0].Events[0].Kind != eventlog.KindRepaired {
		t.Fatalf("inbox = %+v, want the repaired event handed to the agent", got.Pages)
	}
}
