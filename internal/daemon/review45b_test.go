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
