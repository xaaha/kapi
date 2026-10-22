package daemon

import (
	"path/filepath"
	"testing"
)

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
