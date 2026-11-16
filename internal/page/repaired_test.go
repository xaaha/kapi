package page

import (
	"slices"
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/eventlog"
)

func repairedEvents(t *testing.T, l *eventlog.Log) []eventlog.Event {
	t.Helper()
	events, _ := l.After(0)
	return slices.DeleteFunc(events, func(e eventlog.Event) bool { return e.Kind != eventlog.KindRepaired })
}

func TestAWriteThatNeedsRepairsTellsTheAgentOnce(t *testing.T) {
	kv := `<kapi-kv data-kapi-id="aaaaaaaa"><dl><dt>a</dt><dd>b</dd></dl></kapi-kv>`
	home, path := newPage(t, kv+`<p data-kapi-id="bbbbbbbb">one</p>`)
	l := pageLog(t, home, path)
	s, err := Open(home, path, l)
	if err != nil {
		t.Fatal(err)
	}
	got := repairedEvents(t, l)
	if len(got) != 1 || got[0].Author != "kapi" || !strings.Contains(got[0].Text, "#aaaaaaaa <kapi-kv> around a <dl>") {
		t.Fatalf("after opening a page with a mistake, repaired events = %+v", got)
	}

	externalWrite(t, path, kv+`<p data-kapi-id="bbbbbbbb">two</p>`)
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := repairedEvents(t, l); len(got) != 1 {
		t.Fatalf("an edit that left the same mistake in place repeated the note: %+v", got)
	}

	externalWrite(t, path, kv+`<p data-kapi-id="bbbbbbbb">two</p><kapi-note data-kapi-id="cccccccc">x</kapi-note>`)
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	got = repairedEvents(t, l)
	if len(got) != 2 || strings.Contains(got[1].Text, "kapi-kv") || !strings.Contains(got[1].Text, "#cccccccc <kapi-note> is not a preset") {
		t.Fatalf("a new mistake should be reported alone: %+v", got)
	}

	externalWrite(t, path, `<p data-kapi-id="bbbbbbbb">clean</p>`)
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := repairedEvents(t, l); len(got) != 2 {
		t.Fatalf("a clean page needs no repaired event: %+v", got)
	}
}
