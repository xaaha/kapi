package eventlog

import (
	"os"
	"path/filepath"
	"slices"
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

func TestCorruptCursorsFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cursorsFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Error("Open accepted a corrupt cursors file, which would replay every event")
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
		name, cursors string
		events        bool
	}
	var syncs []syncAt
	orig := syncFile
	syncFile = func(f *os.File) error {
		b, _ := os.ReadFile(filepath.Join(dir, cursorsFile))
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
	tmp := slices.IndexFunc(syncs, func(s syncAt) bool { return s.name != dir && s.cursors == "" })
	after := slices.IndexFunc(syncs, func(s syncAt) bool { return s.name == dir && s.cursors == `{"agent":1}` })
	if tmp < 0 || after < tmp {
		t.Errorf("Ack must sync the new cursors file before its rename and %s after it, synced %+v", dir, syncs)
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
