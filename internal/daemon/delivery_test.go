package daemon

import (
	"slices"
	"testing"
)

func texts(r *Response) []string {
	var out []string
	for _, e := range r.Events {
		out = append(out, e.Text)
	}
	return out
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
