package daemon

import "testing"

func TestReview45bSeenRefusesANumberNothingHandedOver(t *testing.T) {
	home, s, _ := serving(t)
	pth := writePage(t, "<p>x</p>")
	l, err := s.page(pth)
	if err != nil {
		t.Fatal(err)
	}
	shown := call(t, home, &Request{Op: "append", Page: pth, Author: "human", Kind: "message", Text: "shown"}).Event.Seq
	call(t, home, &Request{Op: "queue", Page: pth, Reader: "agent", Seq: shown})
	never := call(t, home, &Request{Op: "append", Page: pth, Author: "human", Kind: "message", Text: "never shown"}).Event.Seq
	got := call(t, home, &Request{Op: "seen", Page: pth, Reader: "agent", Seq: never})
	if got.Error == "" && l.Cursor("agent") >= never {
		t.Errorf("seen %d accepted although only #%d was ever handed over; the page shows 👀 on an unshown message", never, shown)
	}
	if got := call(t, home, &Request{Op: "seen", Page: pth, Reader: "agent", Seq: shown}); got.Error != "" || l.Cursor("agent") != shown {
		t.Errorf("seen %d = %+v, cursor %d; want the handed over message seen", shown, got, l.Cursor("agent"))
	}
}

func TestReview45bSayWithSeenPastTheEndDoesNotCoverAMessageThatArrivedMeanwhile(t *testing.T) {
	home, s, _ := serving(t)
	pth := writePage(t, "<p>x</p>")
	l, err := s.page(pth)
	if err != nil {
		t.Fatal(err)
	}
	shown := call(t, home, &Request{Op: "append", Page: pth, Author: "human", Kind: "message", Text: "shown"}).Event.Seq
	call(t, home, &Request{Op: "queue", Page: pth, Reader: "agent", Seq: shown})
	meanwhile := call(t, home, &Request{Op: "append", Page: pth, Author: "human", Kind: "message", Text: "arrived while the agent typed"}).Event.Seq
	got := call(t, home, &Request{Op: "append", Page: pth, Author: "agent", Kind: "message", Text: "reply", Seq: 999})
	if got.Error == "" && l.Cursor("agent") >= meanwhile {
		t.Errorf("say --seen 999 silently marked #%d seen; kapi seen 999 is refused", meanwhile)
	}
	if got.Error == "" {
		t.Error("say --seen 999 was accepted instead of refused")
	}
	if events, _ := l.After(meanwhile); len(events) != 0 {
		t.Errorf("a refused reply was still sent: %+v", events)
	}
}
