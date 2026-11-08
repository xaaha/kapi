package eventlog

import "testing"

func TestWaitingIsAHumanEventTheReaderHasNotSeen(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		if got := l.Waiting("agent"); got != want {
			t.Errorf("Waiting = %v, want %v", got, want)
		}
		got, err := WaitingIn(dir, "agent")
		if err != nil || got != want {
			t.Errorf("WaitingIn = %v, %v, want %v", got, err, want)
		}
	}
	check(false)
	if _, err := l.Append(&Event{Author: "agent", Kind: "message", Text: "done"}); err != nil {
		t.Fatal(err)
	}
	check(false)
	e, err := l.Append(&Event{Author: "human", Kind: "message", Text: "one more thing"})
	if err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := l.Ack("agent", e.Seq); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
