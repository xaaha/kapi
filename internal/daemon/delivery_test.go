package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func texts(r *Response) []string {
	var out []string
	for i := range r.Events {
		out = append(out, r.Events[i].Text)
	}
	return out
}

func TestWaitRepliesOnceAnUnseenEventIsNotQueuedAndRepeatsTheUnseenOnes(t *testing.T) {
	home, _, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	first := call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "first"}).Event.Seq
	if got := call(t, home, &Request{Op: "wait", Page: path, Reader: "agent"}); !slices.Equal(texts(&got), []string{"first"}) {
		t.Fatalf("wait = %+v, want the waiting message", got)
	}
	call(t, home, &Request{Op: "queue", Page: path, Reader: "agent", Seq: first})

	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "wait", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	replies := make(chan Response, 1)
	go func() {
		var r Response
		if json.NewDecoder(conn).Decode(&r) == nil {
			replies <- r
		}
	}()
	select {
	case r := <-replies:
		t.Fatalf("wait replied %+v although everything unseen was already queued", r)
	case <-time.After(300 * time.Millisecond):
	}
	call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "second"})
	select {
	case r := <-replies:
		if !slices.Equal(texts(&r), []string{"first", "second"}) {
			t.Fatalf("wait = %v, want the queued but unseen message again with the new one", texts(&r))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not reply to a new message")
	}
}

func TestOnlyTheNumberTheAgentStatesMovesSeen(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	read := call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "read"}).Event.Seq
	call(t, home, &Request{Op: "queue", Page: path, Reader: "agent", Seq: read})
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "unrelated"})
	if c := l.Cursor("agent"); c != 0 {
		t.Fatalf("seen = %d after a reply that named no event, want 0", c)
	}
	call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "not printed"})
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "reply", Seq: read})
	if c := l.Cursor("agent"); c != read {
		t.Fatalf("seen = %d, want #%d, the event the reply named", c, read)
	}
	if got := call(t, home, &Request{Op: "seen", Page: path, Reader: "agent"}); got.Error == "" {
		t.Error("seen without a number was accepted")
	}
	if got := call(t, home, &Request{Op: "pending", Page: path, Reader: "agent"}); !slices.Equal(texts(&got), []string{"not printed"}) {
		t.Fatalf("pending = %v", texts(&got))
	}
	if got := call(t, home, &Request{Op: "pending", Page: path, Reader: "agent"}); len(got.Events) != 0 {
		t.Fatalf("pending again = %v, want nothing once pending showed it", texts(&got))
	}
}

func TestInboxHandsOverUnseenEventsOfPagesUnderTheDirectory(t *testing.T) {
	home, _, _ := serving(t)
	dir := t.TempDir()
	inside := filepath.Join(dir, "sub", "plan.html")
	if err := os.MkdirAll(filepath.Dir(inside), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := writePage(t, "<p>x</p>")
	for _, p := range []string{inside, outside} {
		call(t, home, &Request{Op: "append", Page: p, Author: "human", Kind: "message", Text: "hello"})
	}
	got := call(t, home, &Request{Op: "inbox", Page: dir, Reader: "agent"})
	if len(got.Pages) != 1 || got.Pages[0].Path != inside || len(got.Pages[0].Events) != 1 || got.Pages[0].Events[0].Text != "hello" {
		t.Fatalf("inbox = %+v, want only the page under the directory", got.Pages)
	}
	if got := call(t, home, &Request{Op: "inbox", Page: dir, Reader: "agent"}); len(got.Pages) != 1 {
		t.Errorf("inbox again = %+v, want the same message, since the inbox only reads", got.Pages)
	}
	for _, req := range []Request{{Op: "inbox", Page: "rel", Reader: "agent"}, {Op: "inbox", Page: dir, Reader: "human"}} {
		if got := call(t, home, &req); got.Error == "" {
			t.Errorf("%+v: no error", req)
		}
	}
}
