package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
)

func TestReview45InboxMatchesTheSameDirectoryThroughASymlink(t *testing.T) {
	home, _, _ := serving(t)
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ page, dir string }{{filepath.Join(link, "a.html"), resolved}, {filepath.Join(resolved, "b.html"), link}} {
		call(t, home, &Request{Op: "append", Page: c.page, Author: "human", Kind: "message", Text: "hello"})
		got := call(t, home, &Request{Op: "inbox", Page: c.dir, Reader: "agent"})
		if !slices.ContainsFunc(got.Pages, func(p PageStatus) bool { return p.Path == c.page }) {
			t.Errorf("inbox for %s = %+v, want the message on %s, the same directory", c.dir, got.Pages, c.page)
		}
	}
}

func TestReview45OneBrokenPageDoesNotHideMessagesOnTheOthersInTheInbox(t *testing.T) {
	home, _, stop := serving(t)
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.html"), filepath.Join(dir, "bad.html")
	call(t, home, &Request{Op: "append", Page: bad, Author: "human", Kind: "message", Text: "x"})
	call(t, home, &Request{Op: "append", Page: good, Author: "human", Kind: "message", Text: "hello"})
	stop()
	broken := filepath.Join(home, "pages", page.ID(bad), "queued.json")
	if err := os.WriteFile(broken, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	servingAt(t, home)
	got := call(t, home, &Request{Op: "inbox", Page: dir, Reader: "agent"})
	byPath := map[string]PageStatus{}
	for _, p := range got.Pages {
		byPath[p.Path] = p
	}
	if got.Error != "" || len(got.Pages) != 2 || len(byPath[good].Events) != 1 {
		t.Fatalf("inbox = %+v, error %q; want the message on %s", got.Pages, got.Error, good)
	}
	if !strings.Contains(byPath[bad].Error, broken) {
		t.Errorf("error for the broken page = %q, want it to name %s", byPath[bad].Error, broken)
	}
}

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
	if l.Cursor("agent") >= m {
		t.Errorf("seen = %d after the inbox read; only the hook, once its output is written, may mark #%d seen", l.Cursor("agent"), m)
	}
}
