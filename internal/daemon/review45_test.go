package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
)

func TestReview45InboxDoesNotReportTheAgentsOwnFileEditAsAHumanMessage(t *testing.T) {
	home, s, _ := serving(t)
	pth := writePage(t, "<p>x</p>")
	l, err := s.page(pth)
	if err != nil {
		t.Fatal(err)
	}
	for _, author := range []string{"file", "kapi"} {
		if _, err := l.Append(&eventlog.Event{Author: author, Kind: "revision", Text: "edited in the file"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := call(t, home, &Request{Op: "inbox", Page: filepath.Dir(pth), Reader: "agent"}); len(got.Pages) != 0 {
		t.Fatalf("inbox = %+v; the Stop hook blocks and says the human wrote, for an edit the agent made", got.Pages[0].Events)
	}
	replies := make(chan Response, 1)
	go func() { replies <- call(t, home, &Request{Op: "wait", Page: pth, Reader: "agent"}) }()
	select {
	case r := <-replies:
		t.Fatalf("wait woke for the agent's own edit: %+v", r.Events)
	case <-time.After(300 * time.Millisecond):
	}
	call(t, home, &Request{Op: "append", Page: pth, Author: "human", Kind: "message", Text: "hi"})
	select {
	case r := <-replies:
		if len(r.Events) != 1 || r.Events[0].Text != "hi" {
			t.Fatalf("wait = %+v, want only the human's message", r.Events)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not wake for the human's message")
	}
}

func TestReview45AMessageQueuedByWatchIsSeenOnlyOnceAHookHandsItToTheModel(t *testing.T) {
	home, s, _ := serving(t)
	pth := writePage(t, "<p>x</p>")
	l, err := s.page(pth)
	if err != nil {
		t.Fatal(err)
	}
	m := call(t, home, &Request{Op: "append", Page: pth, Author: "human", Kind: "message", Text: "new"}).Event.Seq
	call(t, home, &Request{Op: "queue", Page: pth, Reader: "agent", Seq: m})
	call(t, home, &Request{Op: "append", Page: pth, Author: "agent", Kind: "message", Text: "unrelated reply"})
	if l.Cursor("agent") >= m {
		t.Fatalf("#%d was only printed by watch, never reached the model through any hook, and is now seen (page shows 👀)", m)
	}
	got := call(t, home, &Request{Op: "inbox", Page: filepath.Dir(pth), Reader: "agent"})
	if len(got.Pages) != 1 || got.Pages[0].Events[0].Seq != m {
		t.Fatalf("inbox = %+v, want the watch-queued #%d handed to the model", got.Pages, m)
	}
	if l.Cursor("agent") < m {
		t.Errorf("seen = %d after the hook put #%d in the model's context", l.Cursor("agent"), m)
	}
}
