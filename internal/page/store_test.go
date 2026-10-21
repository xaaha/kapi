package page

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func newPage(t *testing.T, src string) (home, path string) {
	t.Helper()
	dir := t.TempDir()
	home, path = filepath.Join(dir, "home"), filepath.Join(dir, "plan.html")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, path
}

func open(t *testing.T, home, path string) *Store {
	t.Helper()
	s, err := Open(home, path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func onDisk(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func externalWrite(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func externalWriteKeepingStamp(t *testing.T, path, src string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func TestAnEditWithTheSameSizeAndMtimeIsNotOverwritten(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s := open(t, home, path)
	externalWriteKeepingStamp(t, path, `<p data-kapi-id="aaaaaaaa">b</p>`)
	if _, _, err := s.Apply(1, Op{Kind: "done", ID: "aaaaaaaa"}); !errors.Is(err, ErrStale) {
		t.Fatalf("Apply over a same-stamp edit: err = %v, want ErrStale", err)
	}
	if got := onDisk(t, path); got != `<p data-kapi-id="aaaaaaaa">b</p>` {
		t.Fatalf("file = %s, want the external edit kept", got)
	}
	if _, changed, err := s.Refresh(); err != nil || !changed {
		t.Fatalf("Refresh: changed=%v err=%v, want the edit picked up", changed, err)
	}
	if src, rev := s.Source(); rev != 2 || string(src) != `<p data-kapi-id="aaaaaaaa">b</p>` {
		t.Errorf("after Refresh rev %d source %s", rev, src)
	}
}

func mustApply(t *testing.T, s *Store, op Op) Summary {
	t.Helper()
	_, rev := s.Source()
	got, sum, err := s.Apply(rev, op)
	if err != nil {
		t.Fatalf("Apply(%+v): %v", op, err)
	}
	if got != rev+1 {
		t.Fatalf("Apply(%+v) rev = %d, want %d", op, got, rev+1)
	}
	return sum
}

var idPattern = regexp.MustCompile(`data-kapi-id="([a-z2-7]{8})"`)

func TestOpenAddsMissingIDsAndWritesThemBack(t *testing.T) {
	home, path := newPage(t, "<h1>Plan</h1>\n<p data-kapi-id=\"keepme22\">Intro <em>here</em></p>\n<kapi-plan>\n  <kapi-step>one</kapi-step>\n</kapi-plan>\n")
	s := open(t, home, path)
	src, rev := s.Source()
	if rev != 1 {
		t.Errorf("first rev = %d, want 1", rev)
	}
	if got := onDisk(t, path); got != string(src) {
		t.Fatalf("file not written back:\n%s\nsource:\n%s", got, src)
	}
	ids := idPattern.FindAllStringSubmatch(string(src), -1)
	if len(ids) != 4 {
		t.Fatalf("want ids on h1, p, kapi-plan, kapi-step; got %d in\n%s", len(ids), src)
	}
	if !strings.Contains(string(src), `<p data-kapi-id="keepme22">Intro <em>here</em></p>`) {
		t.Errorf("existing id or nested prose changed:\n%s", src)
	}

	again := open(t, home, path)
	src2, rev2 := again.Source()
	if rev2 != 1 || !bytes.Equal(src2, src) {
		t.Errorf("reopen changed the page: rev %d\n%s", rev2, src2)
	}
}

func TestOpenGivesADuplicatedIDAFreshOne(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">one</p><p data-kapi-id="aaaaaaaa">copy</p>`)
	src, _ := open(t, home, path).Source()
	ids := idPattern.FindAllStringSubmatch(string(src), -1)
	if len(ids) != 2 || ids[0][1] != "aaaaaaaa" || ids[1][1] == "aaaaaaaa" {
		t.Fatalf("ids = %v in %s", ids, src)
	}
}

func TestApplyRefusesAStaleRevision(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">one</p>`)
	s := open(t, home, path)
	mustApply(t, s, Op{Kind: "done", ID: "aaaaaaaa"})
	before := onDisk(t, path)
	rev, _, err := s.Apply(1, Op{Kind: "delete", ID: "aaaaaaaa"})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if rev != 2 {
		t.Errorf("stale Apply returned rev %d, want current 2", rev)
	}
	if onDisk(t, path) != before {
		t.Error("stale Apply changed the file")
	}
}

func TestEditReplacesTheElementAndKeepsItsID(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">old</p>\n<p data-kapi-id=\"bbbbbbbb\">keep</p>\n")
	s := open(t, home, path)
	sum := mustApply(t, s, Op{Kind: "edit", ID: "aaaaaaaa", HTML: "<kapi-callout kind=\"note\">new <kapi-code lang=\"go\">x</kapi-code></kapi-callout>"})
	got := onDisk(t, path)
	if !strings.HasPrefix(got, `<kapi-callout data-kapi-id="aaaaaaaa" kind="note">new <kapi-code data-kapi-id="`) || !strings.HasSuffix(got, "</kapi-callout>\n<p data-kapi-id=\"bbbbbbbb\">keep</p>\n") {
		t.Fatalf("file after edit:\n%s", got)
	}
	if sum.Text != "edited" || len(sum.IDs) != 1 || sum.IDs[0] != "aaaaaaaa" {
		t.Errorf("summary = %+v", sum)
	}
	snap, err := os.ReadFile(filepath.Join(s.dir, "revs", "1.html"))
	if err != nil || !strings.Contains(string(snap), ">old</p>") {
		t.Errorf("revs/1.html = %q, %v; want the source before the edit", snap, err)
	}
}

func TestEditRejectsBadHTML(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p><p data-kapi-id="bbbbbbbb">b</p>`)
	s := open(t, home, path)
	for _, html := range []string{
		"<p>one</p><p>two</p>",
		"text only",
		`<p data-kapi-id="cccccccc">other id</p>`,
		`<div><kapi-code data-kapi-id="bbbbbbbb">x</kapi-code></div>`,
	} {
		if _, _, err := s.Apply(1, Op{Kind: "edit", ID: "aaaaaaaa", HTML: html}); err == nil {
			t.Errorf("edit with %q succeeded, want an error", html)
		}
	}
	if _, rev := s.Source(); rev != 1 {
		t.Errorf("rev = %d after rejected edits, want 1", rev)
	}
}

func TestDeleteRefusesWhileAnotherElementLinksToIt(t *testing.T) {
	home, path := newPage(t, "<ul data-kapi-id=\"aaaaaaaa\"><li><kapi-callout data-kapi-id=\"bbbbbbbb\">x <a href=\"#bbbbbbbb\">self</a></kapi-callout></li></ul>\n<p data-kapi-id=\"cccccccc\">see <a href=\"#bbbbbbbb\">it</a></p>\n")
	s := open(t, home, path)
	for _, id := range []string{"aaaaaaaa", "bbbbbbbb"} {
		if _, _, err := s.Apply(1, Op{Kind: "delete", ID: id}); !errors.Is(err, ErrInboundLinks) {
			t.Fatalf("delete #%s: err = %v, want ErrInboundLinks", id, err)
		}
	}
	mustApply(t, s, Op{Kind: "delete", ID: "cccccccc"})
	mustApply(t, s, Op{Kind: "delete", ID: "aaaaaaaa"})
	if got := onDisk(t, path); got != "" {
		t.Errorf("file after deletes = %q, want empty", got)
	}
}

func TestDoneAndArchiveSetState(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa" class="x">a</p><kapi-step data-kapi-id="bbbbbbbb" data-kapi-state="done">b</kapi-step>`)
	s := open(t, home, path)
	mustApply(t, s, Op{Kind: "done", ID: "aaaaaaaa"})
	mustApply(t, s, Op{Kind: "archive", ID: "bbbbbbbb"})
	want := `<p data-kapi-state="done" data-kapi-id="aaaaaaaa" class="x">a</p><kapi-step data-kapi-id="bbbbbbbb" data-kapi-state="archived">b</kapi-step>`
	if got := onDisk(t, path); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestMoveBeforeAndToTheEnd(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n<kapi-plan data-kapi-id=\"cccccccc\">\n  <kapi-step data-kapi-id=\"dddddddd\">d</kapi-step>\n  <kapi-step data-kapi-id=\"eeeeeeee\">e</kapi-step>\n</kapi-plan>\n")
	s := open(t, home, path)
	mustApply(t, s, Op{Kind: "move", ID: "bbbbbbbb", Before: "aaaaaaaa"})
	mustApply(t, s, Op{Kind: "move", ID: "bbbbbbbb"})
	mustApply(t, s, Op{Kind: "move", ID: "dddddddd"})
	want := "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<kapi-plan data-kapi-id=\"cccccccc\">\n  <kapi-step data-kapi-id=\"eeeeeeee\">e</kapi-step>\n  <kapi-step data-kapi-id=\"dddddddd\">d</kapi-step>\n</kapi-plan>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n"
	if got := onDisk(t, path); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if _, _, err := s.Apply(4, Op{Kind: "move", ID: "cccccccc", Before: "eeeeeeee"}); err == nil {
		t.Error("moving an element before its own child succeeded")
	}
}

func TestUndoWalksBackThroughRevisions(t *testing.T) {
	orig := "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n"
	home, path := newPage(t, orig)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, Op{Kind: "undo"}); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo on a fresh page: err = %v", err)
	}
	mustApply(t, s, Op{Kind: "delete", ID: "aaaaaaaa"})
	afterDelete := onDisk(t, path)
	mustApply(t, s, Op{Kind: "done", ID: "bbbbbbbb"})

	mustApply(t, s, Op{Kind: "undo"})
	if got := onDisk(t, path); got != afterDelete {
		t.Fatalf("first undo =\n%s\nwant\n%s", got, afterDelete)
	}
	mustApply(t, s, Op{Kind: "undo"})
	if got := onDisk(t, path); got != orig {
		t.Fatalf("second undo =\n%s\nwant\n%s", got, orig)
	}
	src, rev := open(t, home, path).Source()
	if rev != 5 || string(src) != orig {
		t.Errorf("after reopen rev = %d, source =\n%s", rev, src)
	}
}

func TestRefreshPicksUpAnExternalEdit(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	s := open(t, home, path)
	if _, changed, err := s.Refresh(); err != nil || changed {
		t.Fatalf("Refresh with no edit: changed=%v err=%v", changed, err)
	}

	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b, edited</p>\n")
	if _, _, err := s.Apply(1, Op{Kind: "done", ID: "aaaaaaaa"}); !errors.Is(err, ErrStale) {
		t.Fatalf("Apply over an unseen file edit: err = %v, want ErrStale", err)
	}
	sum, changed, err := s.Refresh()
	if err != nil || !changed {
		t.Fatalf("Refresh: changed=%v err=%v", changed, err)
	}
	if len(sum.IDs) != 1 || sum.IDs[0] != "bbbbbbbb" {
		t.Errorf("summary ids = %v, want [bbbbbbbb]", sum.IDs)
	}
	if _, rev := s.Source(); rev != 2 {
		t.Errorf("rev = %d, want 2", rev)
	}

	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<h2>new</h2>\n")
	sum, changed, err = s.Refresh()
	if err != nil || !changed || sum.IDs != nil {
		t.Fatalf("structural edit: summary %+v changed=%v err=%v; want no ids so the page refetches", sum, changed, err)
	}
	if _, h2, _ := strings.Cut(onDisk(t, path), "<h2"); !idPattern.MatchString(h2) {
		t.Errorf("new h2 got no id: %s", onDisk(t, path))
	}

	mustApply(t, s, Op{Kind: "undo"})
	if got := onDisk(t, path); !strings.Contains(got, "b, edited") {
		t.Errorf("undo of an external edit =\n%s", got)
	}
}

func TestOpenNoticesAnEditMadeWhileClosed(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	open(t, home, path)
	externalWrite(t, path, `<p data-kapi-id="aaaaaaaa">changed</p>`)
	s := open(t, home, path)
	if _, rev := s.Source(); rev != 2 {
		t.Fatalf("rev = %d, want 2", rev)
	}
	mustApply(t, s, Op{Kind: "undo"})
	if got := onDisk(t, path); got != `<p data-kapi-id="aaaaaaaa">a</p>` {
		t.Errorf("undo = %s", got)
	}
}

func TestApplyUnknownIDOrKind(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, Op{Kind: "delete", ID: "zzzzzzzz"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Apply(1, Op{Kind: "explode", ID: "aaaaaaaa"}); err == nil {
		t.Error("unknown kind succeeded")
	}
}

func TestOpenNeedsAnAbsoluteExistingPath(t *testing.T) {
	home := t.TempDir()
	if _, err := Open(home, "plan.html"); err == nil {
		t.Error("relative path accepted")
	}
	if _, err := Open(home, filepath.Join(home, "missing.html")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v", err)
	}
}

func TestFormIDAndOuter(t *testing.T) {
	src := []byte(`<kapi-form data-kapi-id="aaaaaaaa" name="env"><kapi-text data-kapi-id="bbbbbbbb" name="why">Why</kapi-text></kapi-form>`)
	if id, err := FormID(src, "env"); err != nil || id != "aaaaaaaa" {
		t.Errorf("FormID = %q, %v", id, err)
	}
	if _, err := FormID(src, "why"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FormID of a non-form: err = %v", err)
	}
	if got, err := Outer(src, "bbbbbbbb"); err != nil || string(got) != `<kapi-text data-kapi-id="bbbbbbbb" name="why">Why</kapi-text>` {
		t.Errorf("Outer = %q, %v", got, err)
	}
	if _, err := Outer(src, "cccccccc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Outer of an unknown id: err = %v", err)
	}
}
