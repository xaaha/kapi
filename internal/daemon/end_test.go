package daemon

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/xaaha/kapi/internal/eventlog"
)

func TestD25AWatchFlushesHeldResolvesBeforeTheEndAndSkipsALiftedEnd(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendAll([]eventlog.Event{
		{Author: "human", Kind: eventlog.KindNote, Target: "a", Text: "why?"},
		{Author: "human", Kind: eventlog.KindResolve, Re: []uint64{1}},
		{Author: "human", Kind: eventlog.KindEnd},
	}); err != nil {
		t.Fatal(err)
	}
	kinds := func(want int) []string {
		t.Helper()
		conn, err := net.Dial("unix", SocketPath(home))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := json.NewEncoder(conn).Encode(Request{Op: "watch", Page: path, Reader: "agent"}); err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(conn)
		var got []string
		for range want {
			var r Response
			if err := dec.Decode(&r); err != nil {
				t.Fatal(err)
			}
			got = append(got, r.Event.Kind)
		}
		silent(t, conn)
		return got
	}
	if got := kinds(3); got[0] != eventlog.KindNote || got[1] != eventlog.KindResolve || got[2] != eventlog.KindEnd {
		t.Fatalf("watch on an ended page delivered %v, want note, resolve, end", got)
	}
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "back"}); err != nil {
		t.Fatal(err)
	}
	if got := kinds(3); got[2] != "message" {
		t.Fatalf("watch after a new message delivered %v, want the lifted end left out", got)
	}
}

func TestD25WaitWakesForAnEndAndInboxAndPagesMarkTheEndedPage(t *testing.T) {
	home, s, _ := serving(t)
	path := writePage(t, "<p>x</p>")
	l, err := s.page(path)
	if err != nil {
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
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindEnd}); err != nil {
		t.Fatal(err)
	}
	var got Response
	if err := json.NewDecoder(conn).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0].Kind != eventlog.KindEnd {
		t.Fatalf("wait returned %+v, want the end", got.Events)
	}
	ended := func() (inbox, pages bool) {
		t.Helper()
		in := call(t, home, &Request{Op: "inbox", Page: "/", Reader: "agent"})
		all := call(t, home, &Request{Op: "pages"})
		if len(in.Pages) != 1 || len(all.Pages) != 1 {
			t.Fatalf("inbox %+v, pages %+v", in.Pages, all.Pages)
		}
		return in.Pages[0].Ended, all.Pages[0].Ended
	}
	if inbox, pages := ended(); !inbox || !pages {
		t.Errorf("ended page: inbox Ended %v, pages Ended %v; want both", inbox, pages)
	}
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "back"}); err != nil {
		t.Fatal(err)
	}
	if inbox, pages := ended(); inbox || pages {
		t.Errorf("after a new message: inbox Ended %v, pages Ended %v; want neither", inbox, pages)
	}
}
