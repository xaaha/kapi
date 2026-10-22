package page

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const r51start = "<p data-kapi-id=\"aaaaaaaa\">start</p>\n<p data-kapi-id=\"bbbbbbbb\">two</p>\n"

func r51left(t *testing.T, path string) []string {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-*"))
	return left
}

func r51holdAtSwap(t *testing.T, path string) **os.File {
	var f *os.File
	replaceHook = func(stage string) {
		if stage == "swap" && f == nil {
			var err error
			if f, err = os.OpenFile(path, os.O_WRONLY, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	return &f
}

func r51write(t *testing.T, f *os.File, body string) {
	t.Helper()
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte(body), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestReview51CtimeOnlyChangeToHeldFileRevertsTheOp(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	after := onDisk(t, path)
	if len(s.held) == 0 {
		t.Fatal("no held file after Apply")
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.Chmod(firstHeld(s), 0o600); err != nil {
		t.Fatal(err)
	}
	tick(t, s, 1)
	if got := onDisk(t, path); got != after {
		t.Errorf("after chmod of the held file and one refresh, page = %q, want the human's op kept %q", got, after)
	}
}

func TestReview51CtimeOnlyChangeToPageDropsALateWrite(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	time.Sleep(5 * time.Millisecond)
	r51write(t, *fp, late)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	tick(t, s, 1)
	if got := onDisk(t, path); got != late {
		t.Errorf("page = %q, want the agent's late write %q", got, late)
	}
}

func TestReview51ThreeQuickRefreshesRetireTheHeldFileInMilliseconds(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	start := time.Now()
	tick(t, s, 3)
	elapsed := time.Since(start)
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	time.Sleep(20 * time.Millisecond)
	r51write(t, *fp, late)
	tick(t, s, 1)
	if got := onDisk(t, path); got != late {
		t.Errorf("held file retired after %v; a write 20ms later is lost: page = %q", elapsed, got)
	}
}

func TestReview51ASecondOpDeletesTheHeldFileOfTheFirst(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	time.Sleep(5 * time.Millisecond)
	r51write(t, *fp, late)
	tick(t, s, 1)
	if got := onDisk(t, path); got != late {
		t.Errorf("page = %q, want the agent's last write %q", got, late)
	}
}

func TestReview51BothChangedTheLaterHeldWriteIsDeletedUnkept(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	agentRename(t, path, "<p data-kapi-id=\"aaaaaaaa\">renamed</p>\n")
	time.Sleep(20 * time.Millisecond)
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	r51write(t, *fp, late)
	tick(t, s, 1)
	got := onDisk(t, path)
	kept, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-kept-*"))
	if got != late && len(kept) == 0 {
		t.Errorf("page = %q and no kapi-kept file; the agent's later write %q is gone (left: %v)", got, late, r51left(t, path))
	}
}

func TestReview51RefreshRetryDeletesARestoreStageLateWrite(t *testing.T) {
	for _, gap := range []time.Duration{0, 5 * time.Millisecond} {
		home, path := newPage(t, r51start)
		s := open(t, home, path)
		agentRename(t, path, "<p>agent one</p>\n")
		var f *os.File
		renamed := false
		replaceHook = func(stage string) {
			if stage == "swap" && !renamed {
				renamed = true
				agentRename(t, path, "<p>agent two</p>\n")
			}
			if stage == "restore" && f == nil {
				var err error
				if f, err = os.OpenFile(path, os.O_WRONLY, 0); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, _, err := s.Refresh(); err != nil {
			t.Fatal(err)
		}
		replaceHook = nil
		if f == nil {
			t.Fatal("restore never ran")
		}
		time.Sleep(gap)
		late := "<p data-kapi-id=\"cccccccc\">late</p>\n"
		r51write(t, f, late)
		tick(t, s, 1)
		if got := onDisk(t, path); got != late {
			t.Errorf("gap %v: page = %q after a refresh, want the agent's last write %q", gap, got, late)
		}
	}
}

func TestReview51OpenWithNoRefreshLeavesTheHeldFile(t *testing.T) {
	home, path := newPage(t, "<p>needs an id</p>\n")
	open(t, home, path)
	time.Sleep(50 * time.Millisecond)
	if left := r51left(t, path); len(left) > 0 {
		t.Errorf("after Open with no refresh, files left next to the page: %v", left)
	}
}

func TestReview51KillAfterSwapBackDropsTheNewestWrite(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	r51write(t, *fp, "<p data-kapi-id=\"aaaaaaaa\">late</p>\n")
	newest := "<p data-kapi-id=\"aaaaaaaa\">newest</p>\n"
	orig := swapFiles
	swapFiles = func(a, b string) error {
		swapFiles = orig
		agentRename(t, path, newest)
		if err := orig(a, b); err != nil {
			return err
		}
		return errKilled
	}
	_, _, err := s.Refresh()
	swapFiles = orig
	if !errors.Is(err, errKilled) {
		t.Fatalf("Refresh err = %v, want the injected kill", err)
	}
	open(t, home, path)
	kept, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-kept-*"))
	if got := onDisk(t, path); got != newest && len(kept) == 0 {
		t.Errorf("after the kill and a reopen, page = %q and nothing kept; the agent's newest write %q is gone", got, newest)
	}
}

func TestReview51OpenDeletesUnknownHeldBytesUnkept(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	agentRename(t, path, "<p data-kapi-id=\"aaaaaaaa\">renamed</p>\n")
	time.Sleep(20 * time.Millisecond)
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	r51write(t, *fp, late)
	open(t, home, path)
	kept, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-kept-*"))
	if got := onDisk(t, path); got != late && len(kept) == 0 {
		t.Errorf("after a reopen, page = %q and no kapi-kept file; the agent's later write %q is deleted", got, late)
	}
}

var errKilled = errors.New("killed")

func firstHeld(s *Store) string { return filepath.Join(s.heldDir(), s.held[0].Name) }
