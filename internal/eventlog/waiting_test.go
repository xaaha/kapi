package eventlog

import "testing"

func TestWaitingIsAHumanEventNoAgentReaderHasSeen(t *testing.T) {
	for _, reader := range []string{"agent", "codex"} {
		t.Run(reader, func(t *testing.T) {
			dir := t.TempDir()
			l, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			check := func(want bool) {
				t.Helper()
				if got := l.Waiting(); got != want {
					t.Errorf("Waiting = %v, want %v", got, want)
				}
				if got, err := WaitingIn(dir); err != nil || got != want {
					t.Errorf("WaitingIn = %v, %v, want %v", got, err, want)
				}
			}
			check(false)
			if _, err := l.Append(&Event{Author: reader, Kind: "message", Text: "done"}); err != nil {
				t.Fatal(err)
			}
			check(false)
			e, err := l.Append(&Event{Author: "human", Kind: "message", Text: "please review"})
			if err != nil {
				t.Fatal(err)
			}
			check(true)
			if err := l.Ack(reader, e.Seq); err != nil {
				t.Fatal(err)
			}
			check(false)
			if _, err := l.Append(&Event{Author: "human", Kind: "message", Text: "one more"}); err != nil {
				t.Fatal(err)
			}
			check(true)
		})
	}
}
