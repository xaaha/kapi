package daemon

import (
	"encoding/json"
	"net"
	"slices"
	"testing"
	"time"
)

func texts(r *Response) []string {
	var out []string
	for _, e := range r.Events {
		out = append(out, e.Text)
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

func TestAReplyMarksSeenOnlyWhatWasQueued(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	printed := call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "printed"}).Event.Seq
	call(t, home, &Request{Op: "queue", Page: path, Reader: "agent", Seq: printed})
	call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "not printed"})
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "reply"})
	if c := l.Cursor("agent"); c != printed {
		t.Fatalf("seen = %d, want #%d, the last event handed to the agent", c, printed)
	}
	if got := call(t, home, &Request{Op: "pending", Page: path, Reader: "agent"}); !slices.Equal(texts(&got), []string{"not printed"}) {
		t.Fatalf("pending = %v", texts(&got))
	}
	if got := call(t, home, &Request{Op: "pending", Page: path, Reader: "agent"}); len(got.Events) != 0 {
		t.Fatalf("pending again = %v, want nothing once pending showed it", texts(&got))
	}
}
