package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
)

func TestAReplyNamesOnlyNotesOnItsPage(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	note, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindNote, Target: "a", Text: "why?"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	for _, re := range [][]uint64{{msg.Seq}, {note.Seq, 99}} {
		resp := call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Re: re, Text: "because"})
		if !strings.Contains(resp.Error, "is not a note on this page") {
			t.Errorf("reply to %v: error %q, want a refusal", re, resp.Error)
		}
	}
	if events, _ := l.After(0); len(events) != 2 {
		t.Fatalf("a refused reply was logged: %+v", events)
	}
	resp := call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Re: []uint64{note.Seq}, Text: "because"})
	if resp.Error != "" || resp.Event == nil || len(resp.Event.Re) != 1 || resp.Event.Re[0] != note.Seq {
		t.Fatalf("reply = %+v", resp)
	}
}

func silent(t *testing.T, conn net.Conn) *json.Decoder {
	t.Helper()
	dec := json.NewDecoder(conn)
	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var got Response
	if err := dec.Decode(&got); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("got %+v (%v), want nothing yet", got, err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return json.NewDecoder(conn)
}

func TestAResolveReachesAWatchOnlyWithTheNextEvent(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	note, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindNote, Target: "a", Text: "why?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("agent", note.Seq); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "watch", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	waitPresence(t, l, "agent watching")

	resolve, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindResolve, Re: []uint64{note.Seq}})
	if err != nil {
		t.Fatal(err)
	}
	dec := silent(t, conn)
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "still here"})
	time.Sleep(100 * time.Millisecond)
	if q := l.Queued("agent"); q >= resolve.Seq {
		t.Fatalf("queued through #%d, past the resolve #%d it never printed", q, resolve.Seq)
	}
	call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "next"})
	for _, want := range []string{eventlog.KindResolve, "message"} {
		var got Response
		if err := dec.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Event == nil || got.Event.Kind != want {
			t.Fatalf("watch delivered %+v, want %s", got.Event, want)
		}
	}
}

func TestWaitDoesNotWakeForAResolveAlone(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	note, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindNote, Target: "a", Text: "why?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("agent", note.Seq); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "wait", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	waitPresence(t, l, "agent watching")
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindResolve, Re: []uint64{note.Seq}}); err != nil {
		t.Fatal(err)
	}
	dec := silent(t, conn)
	call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "next"})
	var got Response
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 || got.Events[0].Kind != eventlog.KindResolve || got.Events[1].Text != "next" {
		t.Fatalf("wait returned %+v, want the resolve and the message", got.Events)
	}
}
