package page

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
	lc := s.Lifecycle()
	viewed, touched := lc.Viewed, lc.Touched
	if !viewed.IsZero() || !touched.Equal(written) {
		t.Errorf("viewed %v touched %v, want never viewed and touched at %v", viewed, touched, written)
	}
}

func archive(t *testing.T, s *Store) {
	t.Helper()
	lc := s.Lifecycle()
	if !lc.Archived.IsZero() {
		return
	}
	if ok, err := s.MarkArchived(clock(), lc.Touched); err != nil || !ok {
		t.Fatalf("MarkArchived = %v, %v", ok, err)
	}
}

func TestOnlyMeaningfulActivityMovesTouchedAndBringsThePageBackFromTheArchive(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p><p data-kapi-id="bbbbbbbb">b</p>`)
	set := fixClock(t)
	s := open(t, home, path)
	start := s.Lifecycle().Touched
	base := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	step := 0
	moves := func(how string, act func()) {
		t.Helper()
		step++
		at := base.Add(time.Duration(step) * time.Minute)
		set(at)
		archive(t, s)
		act()
		if lc := s.Lifecycle(); !lc.Touched.Equal(at) || !lc.Archived.IsZero() {
			t.Errorf("%s: touched %v archived %v, want touched %v and back from the archive", how, lc.Touched, lc.Archived, at)
		}
	}
	stays := func(how string, act func()) {
		t.Helper()
		step++
		before := s.Lifecycle().Touched
		set(base.Add(time.Duration(step) * time.Minute))
		archive(t, s)
		act()
		if lc := s.Lifecycle(); !lc.Touched.Equal(before) || lc.Archived.IsZero() {
			t.Errorf("%s moved touched from %v to %v or unarchived it (archived %v)", how, before, lc.Touched, lc.Archived)
		}
	}

	stays("viewing", func() {
		if err := s.MarkViewed(); err != nil {
			t.Fatal(err)
		}
	})
	if touched := s.Lifecycle().Touched; !touched.Equal(start) {
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
	if touched := s.Lifecycle().Touched; !touched.Equal(written) {
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
	if lc := again.Lifecycle(); !lc.Viewed.Equal(looked) || !lc.Touched.Equal(edited) {
		t.Errorf("after reopening, viewed %v touched %v, want %v and %v", lc.Viewed, lc.Touched, looked, edited)
	}
	lc, err := ReadLifecycle(again.dir)
	if err != nil || !lc.Viewed.Equal(looked) || !lc.Touched.Equal(edited) {
		t.Errorf("ReadLifecycle = %+v, %v, want viewed %v and touched %v", lc, err, looked, edited)
	}
}

func TestReadLifecycleOfAPageWithoutStateIsZero(t *testing.T) {
	lc, err := ReadLifecycle(t.TempDir())
	if err != nil || lc != (Lifecycle{}) {
		t.Errorf("ReadLifecycle = %+v, %v", lc, err)
	}
}

func TestArchivingSkipsSavedPagesAndPagesTouchedSinceTheSweepLooked(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	set := fixClock(t)
	s := open(t, home, path)
	seen := s.Lifecycle().Touched
	set(seen.Add(time.Hour))
	if err := s.Touch(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkArchived(clock(), seen); err != nil || ok {
		t.Errorf("archiving with a stale touched time = %v, %v, want skipped", ok, err)
	}
	if ok, err := s.MarkArchived(clock(), s.Lifecycle().Touched); err != nil || !ok {
		t.Fatalf("MarkArchived = %v, %v", ok, err)
	}
	if ok, err := s.MarkArchived(clock().Add(time.Hour), s.Lifecycle().Touched); err != nil || ok {
		t.Errorf("archiving an archived page again = %v, %v, want skipped", ok, err)
	}
	if lc := open(t, home, path).Lifecycle(); !lc.Archived.Equal(clock()) {
		t.Errorf("after reopening, archived %v, want %v", lc.Archived, clock())
	}

	saved, savedPath := newPage(t, `<p data-kapi-id="bbbbbbbb">b</p>`)
	kept := open(t, saved, savedPath)
	state := filepath.Join(kept.dir, "state.json")
	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, append(bytes.TrimSuffix(b, []byte("}")), []byte(`,"saved":true}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, err := ReadLifecycle(kept.dir)
	if err != nil || !lc.Saved {
		t.Fatalf("ReadLifecycle = %+v, %v, want saved", lc, err)
	}
	if ok, err := MarkArchivedAt(kept.dir, clock(), lc.Touched); err != nil || ok {
		t.Errorf("archiving a saved closed page = %v, %v, want skipped", ok, err)
	}
	reopened := open(t, saved, savedPath)
	if ok, err := reopened.MarkArchived(clock(), lc.Touched); err != nil || ok {
		t.Errorf("archiving a saved open page = %v, %v, want skipped", ok, err)
	}
	if err := reopened.MarkViewed(); err != nil {
		t.Fatal(err)
	}
	if lc, _ := ReadLifecycle(kept.dir); !lc.Saved {
		t.Error("the store dropped the saved flag when it rewrote state.json")
	}
}

func TestArchivingAClosedPageKeepsTheRestOfItsState(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	fixClock(t)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p>b</p>`})
	if err := s.MarkViewed(); err != nil {
		t.Fatal(err)
	}
	before := s.Lifecycle()
	at := clock().Add(time.Hour)
	if ok, err := MarkArchivedAt(s.dir, at, before.Touched); err != nil || !ok {
		t.Fatalf("MarkArchivedAt = %v, %v", ok, err)
	}
	again := open(t, home, path)
	want := before
	want.Archived = at
	if lc := again.Lifecycle(); lc != want {
		t.Errorf("after reopening, lifecycle %+v, want %+v", lc, want)
	}
	if src, rev := again.Source(); rev != 2 || !strings.Contains(string(src), ">b</p>") {
		t.Errorf("after archiving, source %q at rev %d, want the edit at rev 2", src, rev)
	}
	if ok, err := MarkArchivedAt(t.TempDir(), at, before.Touched); err != nil || ok {
		t.Errorf("archiving a page with no state = %v, %v, want skipped", ok, err)
	}
}
