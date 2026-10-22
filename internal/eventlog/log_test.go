package eventlog

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		if _, err := l.Append(&Event{Author: "human", Kind: "message", Text: text}); err != nil {
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
	errs := make(chan error, 1)
	go func() {
		_, err := l.Append(&Event{Author: "human", Kind: "message", Text: "hi"})
		errs <- err
	}()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("append did not wake waiter")
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
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
	for _, text := range []string{"a", "b"} {
		if _, err := l.Append(&Event{Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	for _, seq := range []uint64{2, 1} {
		if err := l.Ack("r", seq); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.Cursor("r"); got != 2 {
		t.Fatalf("cursor = %d", got)
	}
	if err := l.Ack("r", 9); err == nil {
		t.Fatal("ack past end should fail")
	}
}

func TestQueuedSurvivesReopenAndNeverTrailsSeen(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"a", "b", "c"} {
		if _, err := l.Append(&Event{Author: "human", Kind: "message", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Queue("agent", 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Queue("agent", 4); err == nil {
		t.Error("queue past the end should fail")
	}
	l.Close()
	if l, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	if q, c := l.Queued("agent"), l.Cursor("agent"); q != 2 || c != 0 {
		t.Fatalf("after reopen queued = %d, seen = %d; want 2 and 0", q, c)
	}
	if len(l.Pending("agent")) != 3 {
		t.Errorf("pending = %+v, want every queued event still unseen", l.Pending("agent"))
	}
	if err := l.Ack("agent", 3); err != nil {
		t.Fatal(err)
	}
	if q := l.Queued("agent"); q != 3 {
		t.Errorf("queued = %d after seeing #3, want 3", q)
	}
}

func TestAHandoverSetsEveryPositionInOneWriteAndSeenStopsBeforeACutEvent(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := l.Append(&Event{Author: "human", Kind: "message", Text: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Queue("agent", 5); err != nil {
		t.Fatal(err)
	}
	if err := l.Handover("agent", 4, []uint64{3}); err != nil {
		t.Fatal(err)
	}
	if err := l.Handover("agent", 9, nil); err == nil {
		t.Error("a handover past the last event was accepted")
	}
	l.Close()
	if l, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	if s, h, q, c := l.Cursor("agent"), l.Hooked("agent"), l.Queued("agent"), l.Cut("agent"); s != 2 || h != 4 || q != 5 || !slices.Equal(c, []uint64{3}) {
		t.Fatalf("after reopen seen %d, hooked %d, queued %d, cut %v; want 2, 4, 5, [3]", s, h, q, c)
	}
	if err := l.Ack("agent", 4); err != nil {
		t.Fatal(err)
	}
	if c := l.Cut("agent"); len(c) != 0 {
		t.Errorf("cut = %v after the agent read through #4, want none", c)
	}
	if _, err := os.Stat(filepath.Join(dir, cursorsFile)); !os.IsNotExist(err) {
		t.Errorf("a separate cursors file was written: %v", err)
	}
}

func TestPositionsFromAnOlderCursorsFileAreKept(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cursorsFile), []byte(`{"agent":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c := l.Cursor("agent"); c != 1 {
		t.Errorf("seen = %d from an older kapi's cursors.json, want 1", c)
	}
}

func TestAnUnreadablePositionsFileFallsBackInsteadOfFailingToOpen(t *testing.T) {
	whole := `{"seq":1,"time":"2026-01-02T03:04:05Z","author":"human","kind":"message","text":"one"}` + "\n" +
		`{"seq":2,"time":"2026-01-02T03:04:06Z","author":"human","kind":"message","text":"two"}` + "\n"
	for _, c := range []struct {
		name    string
		files   map[string]string
		seen    uint64
		pending int
	}{
		{"empty positions", map[string]string{positionsFile: ""}, 0, 2},
		{"torn positions", map[string]string{positionsFile: `{"seen":{"agent":2},"que`}, 0, 2},
		{"torn positions over old cursors", map[string]string{positionsFile: `{"seen":{"age`, cursorsFile: `{"agent":1}`}, 1, 1},
		{"corrupt old cursors", map[string]string{cursorsFile: "{not json"}, 0, 2},
	} {
		dir := t.TempDir()
		c.files[eventsFile] = whole
		for name, body := range c.files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, pending, err := Summary(dir); err != nil || pending("agent") != c.pending {
			t.Errorf("%s: Summary err = %v", c.name, err)
		}
		l, err := Open(dir)
		if err != nil {
			t.Errorf("%s: Open = %v, want the log open with every unseen event pending", c.name, err)
			continue
		}
		if got := l.Cursor("agent"); got != c.seen {
			t.Errorf("%s: seen = %d, want %d", c.name, got, c.seen)
		}
		if err := l.Ack("agent", 2); err != nil {
			t.Errorf("%s: Ack after a fallback: %v", c.name, err)
		}
		l.Close()
	}
}

func TestATornLastLineIsDroppedOnOpen(t *testing.T) {
	dir := t.TempDir()
	whole := `{"seq":1,"time":"2026-01-02T03:04:05Z","author":"human","kind":"message","text":"one"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, eventsFile), []byte(whole+`{"seq":2,"time":"2026-01`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, pending, err := Summary(dir); err != nil || pending("agent") != 1 {
		t.Fatalf("Summary of a log with a torn last line: err = %v", err)
	}
	l, err := Open(dir)
	if err != nil {
		t.Fatalf("Open of a log with a torn last line: %v", err)
	}
	if e, err := l.Append(&Event{Author: "human", Kind: "message", Text: "two"}); err != nil || e.Seq != 2 {
		t.Fatalf("Append after a torn line = %+v, %v", e, err)
	}
	l.Close()
	l, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen after appending past a torn line: %v", err)
	}
	if events, _ := l.After(0); len(events) != 2 || events[1].Text != "two" {
		t.Errorf("events = %+v, want one and two", events)
	}
}

func TestTheLogSyncsItsFilesAndDirectoryBeforeRelyingOnThem(t *testing.T) {
	dir := t.TempDir()
	type syncAt struct {
		name, positions string
		events          bool
	}
	var syncs []syncAt
	orig := syncFile
	syncFile = func(f *os.File) error {
		b, _ := os.ReadFile(filepath.Join(dir, positionsFile))
		_, err := os.Stat(filepath.Join(dir, eventsFile))
		syncs = append(syncs, syncAt{f.Name(), string(b), err == nil})
		return orig(f)
	}
	defer func() { syncFile = orig }()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !slices.Contains(syncs, syncAt{name: dir, events: true}) {
		t.Errorf("Open did not sync %s after creating %s: %+v", dir, eventsFile, syncs)
	}
	if _, err := l.Append(&Event{Author: "human", Kind: "message", Text: "one"}); err != nil {
		t.Fatal(err)
	}
	syncs = nil
	if err := l.Ack("agent", 1); err != nil {
		t.Fatal(err)
	}
	tmp := slices.IndexFunc(syncs, func(s syncAt) bool { return s.name != dir && s.positions == "" })
	after := slices.IndexFunc(syncs, func(s syncAt) bool { return s.name == dir && strings.Contains(s.positions, `"seen":{"agent":1}`) })
	if tmp < 0 || after < tmp {
		t.Errorf("Ack must sync the new positions file before its rename and %s after it, synced %+v", dir, syncs)
	}
}

func TestPresenceIsNeverPending(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{{Kind: KindPresence, Text: "watching"}, {Kind: "message", Text: "hi"}, {Kind: KindPresence, Text: "none"}} {
		if _, err := l.Append(&e); err != nil {
			t.Fatal(err)
		}
	}
	if p := l.Pending("agent"); len(p) != 1 || p[0].Seq != 2 {
		t.Errorf("Pending = %+v, want only the message", p)
	}
	_, pending, err := Summary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := pending("agent"); n != 1 {
		t.Errorf("Summary pending = %d, want 1", n)
	}
	if events, _ := l.After(0); len(events) != 3 {
		t.Errorf("After(0) has %d events, want all 3", len(events))
	}
}

func TestPrintableStripsTerminalControls(t *testing.T) {
	in := "a\x1b]0;owned\x07\x1b[2J\x00b\nc\td\re\x7f\u0085\u009bf\u202eg\u2066h\u2069i é"
	if got, want := Printable(in), "a]0;owned[2Jb c defghi é"; got != want {
		t.Errorf("Printable = %q, want %q", got, want)
	}
}

func TestPrintableStripsMarksAndSeparators(t *testing.T) {
	if got := Printable("a\u200eb\u200fc\u061cd\u2028e\u2029f"); got != "abcdef" {
		t.Errorf("Printable = %q", got)
	}
}
