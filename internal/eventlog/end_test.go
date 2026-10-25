package eventlog

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestD25OnlyAHumanMessageNoteOrAnswerLiftsAnEnd(t *testing.T) {
	end := Event{Seq: 2, Author: "human", Kind: KindEnd}
	for _, c := range []struct {
		name  string
		after []Event
		want  uint64
	}{
		{"nothing after it", nil, 2},
		{"an agent reply", []Event{{Seq: 3, Author: "agent", Kind: "message"}}, 2},
		{"a resolve", []Event{{Seq: 3, Author: "human", Kind: KindResolve}}, 2},
		{"presence", []Event{{Seq: 3, Author: "agent", Kind: KindPresence, Text: "none"}}, 2},
		{"a file edit", []Event{{Seq: 3, Author: "file", Kind: "revision"}}, 2},
		{"a human message", []Event{{Seq: 3, Author: "human", Kind: "message"}}, 0},
		{"a human note", []Event{{Seq: 3, Author: "human", Kind: KindNote}}, 0},
		{"a form answer", []Event{{Seq: 3, Author: "human", Kind: "answer"}}, 0},
		{"a message then a second end", []Event{{Seq: 3, Author: "human", Kind: "message"}, {Seq: 4, Author: "human", Kind: KindEnd}}, 4},
	} {
		events := append([]Event{{Seq: 1, Author: "human", Kind: "message"}, end}, c.after...)
		if got := Ended(events); got != c.want {
			t.Errorf("%s: Ended = %d, want %d", c.name, got, c.want)
		}
	}
	if got := Ended([]Event{{Seq: 1, Author: "human", Kind: "message"}}); got != 0 {
		t.Errorf("a page never ended: %d", got)
	}
}

func TestD25SummaryReportsTheEndInForce(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.AppendAll([]Event{{Author: "human", Kind: "message", Text: "hi"}, {Author: "human", Kind: KindEnd}}); err != nil {
		t.Fatal(err)
	}
	if _, _, ended, err := Summary(dir); err != nil || ended != 2 {
		t.Fatalf("Summary ended = %d, %v; want 2", ended, err)
	}
	if _, err := l.Append(&Event{Author: "human", Kind: "message", Text: "back"}); err != nil {
		t.Fatal(err)
	}
	if _, _, ended, _ := Summary(dir); ended != 0 || l.Ended() != 0 {
		t.Errorf("after a new message: Summary %d, Ended %d; want 0", ended, l.Ended())
	}
}

func TestD25ConcurrentEndsAppendOnlyOne(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checked := endChecked
	endChecked = func() { time.Sleep(5 * time.Millisecond) }
	t.Cleanup(func() { endChecked = checked })
	var wg sync.WaitGroup
	var ok, refused atomic.Int32
	for range 20 {
		wg.Go(func() {
			_, err := l.End("human")
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrEnded):
				refused.Add(1)
			default:
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if events, _ := l.After(0); ok.Load() != 1 || refused.Load() != 19 || len(events) != 1 {
		t.Fatalf("%d ends appended, %d refused, %d events; want 1, 19, 1", ok.Load(), refused.Load(), len(events))
	}
}
