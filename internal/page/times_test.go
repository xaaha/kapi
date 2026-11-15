package page

import (
	"os"
	"testing"
	"time"
)

func fixClock(t *testing.T) func(time.Time) {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Second)
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = time.Now })
	return func(next time.Time) { at = next }
}

func TestANewPageIsTouchedWhenItsFileWasWrittenAndNeverViewed(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	written := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	fixClock(t)
	s := open(t, home, path)
	viewed, touched := s.Times()
	if !viewed.IsZero() || !touched.Equal(written) {
		t.Errorf("viewed %v touched %v, want never viewed and touched at %v", viewed, touched, written)
	}
}

func TestOnlyMeaningfulActivityMovesTouched(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p><p data-kapi-id="bbbbbbbb">b</p>`)
	set := fixClock(t)
	s := open(t, home, path)
	_, start := s.Times()
	base := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	step := 0
	moves := func(how string, act func()) {
		t.Helper()
		step++
		at := base.Add(time.Duration(step) * time.Minute)
		set(at)
		act()
		if _, touched := s.Times(); !touched.Equal(at) {
			t.Errorf("%s: touched %v, want %v", how, touched, at)
		}
	}
	stays := func(how string, act func()) {
		t.Helper()
		step++
		_, before := s.Times()
		set(base.Add(time.Duration(step) * time.Minute))
		act()
		if _, touched := s.Times(); !touched.Equal(before) {
			t.Errorf("%s moved touched from %v to %v", how, before, touched)
		}
	}

	stays("viewing", func() {
		if err := s.MarkViewed(); err != nil {
			t.Fatal(err)
		}
	})
	if _, touched := s.Times(); !touched.Equal(start) {
		t.Fatalf("touched moved from %v to %v before any activity", start, touched)
	}
	stays("a refresh with nothing new", func() {
		if _, _, err := s.Refresh(); err != nil {
			t.Fatal(err)
		}
	})
	moves("a human block edit", func() { mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"}) })
	moves("an undo", func() { mustApply(t, s, &Op{Kind: "undo"}) })
	moves("an agent block op", func() {
		_, rev := s.Source()
		if _, _, err := s.ApplyBy("agent", rev, &Op{Kind: "delete", ID: "bbbbbbbb"}); err != nil {
			t.Fatal(err)
		}
	})
	moves("a human note", func() {
		if err := s.Touch(); err != nil {
			t.Fatal(err)
		}
	})
	step++
	written := base.Add(time.Duration(step) * time.Minute)
	set(written.Add(time.Second))
	if err := os.WriteFile(path, []byte(`<p data-kapi-id="cccccccc">from the agent</p>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := s.Refresh(); err != nil || !changed {
		t.Fatalf("Refresh = %v, %v", changed, err)
	}
	if _, touched := s.Times(); !touched.Equal(written) {
		t.Errorf("an agent writing the file: touched %v, want the file's write time %v", touched, written)
	}
}

func TestViewedAndTouchedSurviveReopening(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	set := fixClock(t)
	s := open(t, home, path)
	edited := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	set(edited)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	looked := edited.Add(time.Minute)
	set(looked)
	if err := s.MarkViewed(); err != nil {
		t.Fatal(err)
	}
	set(looked.Add(time.Hour))
	again := open(t, home, path)
	if viewed, touched := again.Times(); !viewed.Equal(looked) || !touched.Equal(edited) {
		t.Errorf("after reopening, viewed %v touched %v, want %v and %v", viewed, touched, looked, edited)
	}
	viewed, touched, err := ReadTimes(again.dir)
	if err != nil || !viewed.Equal(looked) || !touched.Equal(edited) {
		t.Errorf("ReadTimes = %v, %v, %v, want %v and %v", viewed, touched, err, looked, edited)
	}
}

func TestReadTimesOfAPageWithoutStateIsZero(t *testing.T) {
	viewed, touched, err := ReadTimes(t.TempDir())
	if err != nil || !viewed.IsZero() || !touched.IsZero() {
		t.Errorf("ReadTimes = %v, %v, %v", viewed, touched, err)
	}
}
