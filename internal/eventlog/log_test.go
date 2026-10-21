package eventlog

import (
	"testing"
	"time"
)

func TestAppendSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one", "two", "three"} {
		if _, err := l.Append(Event{Author: "human", Kind: "message", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Ack("agent", 1); err != nil {
		t.Fatal(err)
	}
	l.Close()

	l, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pending := l.Pending("agent")
	if len(pending) != 2 || pending[0].Text != "two" || pending[1].Seq != 3 {
		t.Fatalf("pending after reopen = %+v", pending)
	}
}

func TestAfterWakesOnAppend(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	events, changed := l.After(0)
	if len(events) != 0 {
		t.Fatalf("expected empty log, got %d", len(events))
	}
	go l.Append(Event{Author: "human", Kind: "message", Text: "hi"})
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("append did not wake waiter")
	}
	if events, _ := l.After(0); len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
}

func TestAckIsMonotonic(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l.Append(Event{Text: "a"})
	l.Append(Event{Text: "b"})
	l.Ack("r", 2)
	l.Ack("r", 1)
	if got := l.Cursor("r"); got != 2 {
		t.Fatalf("cursor = %d", got)
	}
	if err := l.Ack("r", 9); err == nil {
		t.Fatal("ack past end should fail")
	}
}
