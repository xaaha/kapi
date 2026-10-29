package page

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/render"
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
	s, err := Open(home, path, pageLog(t, home, path))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pageLog(t *testing.T, home, path string) *eventlog.Log {
	t.Helper()
	l, err := eventlog.Open(filepath.Join(home, "pages", ID(path)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
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
	if _, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa"}); !errors.Is(err, ErrStale) {
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

func TestApplyReplacesTheFileInsteadOfWritingIntoIt(t *testing.T) {
	orig := `<p data-kapi-id="aaaaaaaa">a</p>`
	home, path := newPage(t, orig)
	s := open(t, home, path)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	reader := path + ".reader"
	if err := os.Link(path, reader); err != nil {
		t.Fatal(err)
	}
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	if got := onDisk(t, reader); got != orig {
		t.Errorf("a reader holding the old file saw %q; the write went into the file in place", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", fi.Mode().Perm())
	}
	left, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-*"))
	if err != nil || len(left) != 0 {
		t.Errorf("temp files left behind: %v %v", left, err)
	}
}

func tick(t *testing.T, s *Store, n int) {
	t.Helper()
	for range n {
		if _, _, err := s.Refresh(); err != nil {
			t.Fatal(err)
		}
	}
}

func mustApply(t *testing.T, s *Store, op *Op) Summary {
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
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	before := onDisk(t, path)
	rev, _, err := s.Apply(1, &Op{Kind: "delete", ID: "aaaaaaaa"})
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

func TestAnOpOnAnUntouchedBlockSucceedsAtAnOlderRevision(t *testing.T) {
	const orig = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n<p data-kapi-id=\"cccccccc\">c</p>\n"
	const agents = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n<p data-kapi-id=\"cccccccc\">c, by the agent</p>\n"
	for _, op := range []Op{
		{Kind: "edit", ID: "aaaaaaaa", HTML: `<p>a, by the human</p>`},
		{Kind: "archive", ID: "aaaaaaaa"},
		{Kind: "delete", ID: "aaaaaaaa"},
	} {
		t.Run(op.Kind, func(t *testing.T) {
			home, path := newPage(t, orig)
			s := open(t, home, path)
			externalWrite(t, path, agents)
			if _, _, err := s.Refresh(); err != nil {
				t.Fatal(err)
			}
			rev, _, err := s.Apply(1, &op)
			if err != nil {
				t.Fatalf("%s at rev 1 after the agent changed another block: %v", op.Kind, err)
			}
			if rev != 3 {
				t.Errorf("rev = %d, want 3", rev)
			}
			if got := onDisk(t, path); !strings.Contains(got, "c, by the agent") {
				t.Errorf("the agent's edit was lost:\n%s", got)
			}
		})
	}
}

func TestUndoAtAnOldRevDoesNotUndoAnotherTabsOp(t *testing.T) {
	home, path := newPage(t, "<h1 data-kapi-id=\"hhhhhhhh\">Plan <b>bold</b></h1>\n<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	if _, _, err := s.Apply(1, &Op{Kind: "delete", ID: "bbbbbbbb"}); err != nil {
		t.Fatalf("tab 2 deletes b at rev 1: %v", err)
	}
	for _, id := range []string{"", "hhhhhhhh"} {
		if _, _, err := s.Apply(2, &Op{Kind: "undo", ID: id}); !errors.Is(err, ErrStale) {
			t.Fatalf("tab 1 undoes at rev 2 with id %q: err = %v, want ErrStale", id, err)
		}
	}
	if got := onDisk(t, path); strings.Contains(got, "bbbbbbbb") {
		t.Errorf("a stale undo brought b back:\n%s", got)
	}
}

func TestAMoveAtAnOldRevIsStaleEvenWhenTheBlockIsUnchanged(t *testing.T) {
	const orig = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n<p data-kapi-id=\"cccccccc\">c</p>\n"
	home, path := newPage(t, orig)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "move", ID: "cccccccc", Before: "aaaaaaaa"})
	moved := onDisk(t, path)
	if _, _, err := s.Apply(1, &Op{Kind: "move", ID: "aaaaaaaa", Before: "cccccccc"}); !errors.Is(err, ErrStale) {
		t.Fatalf("move at rev 1 after another tab's move: err = %v, want ErrStale", err)
	}
	if got := onDisk(t, path); got != moved {
		t.Errorf("a stale move changed the order:\n%s", got)
	}
}

func TestOpsWithoutAnIDAreRefused(t *testing.T) {
	const orig = "<h1 data-kapi-id=\"hhhhhhhh\">Plan <b>bold</b></h1>\n"
	for _, kind := range []string{"edit", "archive", "unarchive", "delete", "move"} {
		home, path := newPage(t, orig)
		s := open(t, home, path)
		_, _, err := s.Apply(1, &Op{Kind: kind, HTML: "<b>x</b>"})
		if err == nil || errors.Is(err, ErrStale) {
			t.Errorf("%s with no id: err = %v, want a refusal", kind, err)
		}
		if got := onDisk(t, path); got != orig {
			t.Errorf("%s with no id changed the file:\n%s", kind, got)
		}
	}
}

func TestAnOpOnABlockThatChangedSinceItsRevisionIsStale(t *testing.T) {
	for _, op := range []Op{
		{Kind: "edit", ID: "aaaaaaaa", HTML: `<p>a, by the human</p>`},
		{Kind: "archive", ID: "aaaaaaaa"},
		{Kind: "unarchive", ID: "aaaaaaaa"},
		{Kind: "delete", ID: "aaaaaaaa"},
		{Kind: "move", ID: "aaaaaaaa"},
	} {
		t.Run(op.Kind, func(t *testing.T) {
			home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
			s := open(t, home, path)
			const agents = "<p data-kapi-id=\"aaaaaaaa\">a, by the agent</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n"
			externalWrite(t, path, agents)
			if _, _, err := s.Refresh(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Apply(1, &op); !errors.Is(err, ErrStale) {
				t.Fatalf("%s on a block the agent changed: err = %v, want ErrStale", op.Kind, err)
			}
			if got := onDisk(t, path); got != agents {
				t.Errorf("stale op changed the file:\n%s", got)
			}
		})
	}
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	for _, rev := range []uint64{0, 3} {
		if _, _, err := s.Apply(rev, &Op{Kind: "archive", ID: "aaaaaaaa"}); !errors.Is(err, ErrStale) {
			t.Errorf("rev %d: err = %v, want ErrStale", rev, err)
		}
	}
	if _, _, err := s.Apply(1, &Op{Kind: "undo"}); !errors.Is(err, ErrStale) {
		t.Errorf("undo at an old rev: err = %v, want ErrStale", err)
	}
}

func TestEditReplacesTheElementAndKeepsItsID(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">old</p>\n<p data-kapi-id=\"bbbbbbbb\">keep</p>\n")
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: "<kapi-callout kind=\"note\">new <kapi-code lang=\"go\">x</kapi-code></kapi-callout>"})
	got := onDisk(t, path)
	if !strings.HasPrefix(got, `<kapi-callout data-kapi-id="aaaaaaaa" kind="note">new <kapi-code data-kapi-id="`) || !strings.HasSuffix(got, "</kapi-callout>\n<p data-kapi-id=\"bbbbbbbb\">keep</p>\n") {
		t.Fatalf("file after edit:\n%s", got)
	}
	if sum.Text != `edited callout "new x"` || len(sum.IDs) != 1 || sum.IDs[0] != "aaaaaaaa" {
		t.Errorf("summary = %+v", sum)
	}
	snap, err := os.ReadFile(filepath.Join(s.dir, "revs", "1.html"))
	if err != nil || !strings.Contains(string(snap), ">old</p>") {
		t.Errorf("revs/1.html = %q, %v; want the source before the edit", snap, err)
	}
}

func TestEditRefusesToLeaveDanglingLinks(t *testing.T) {
	const orig = "<p data-kapi-id=\"aaaaaaaa\">a <span data-kapi-id=\"cccccccc\">x</span></p>\n<p data-kapi-id=\"bbbbbbbb\">see <a href=\"#cccccccc\">code</a> and <a href=\"#gone0000\">old</a></p>\n"
	for _, tc := range []struct {
		name, id, html, dangling string
	}{
		{"adds a link to a missing id", "aaaaaaaa", `<p>a <span data-kapi-id="cccccccc">x</span> <a href="#zzzzzzzz">z</a> <a href="#yyyyyyyy">y</a></p>`, "#yyyyyyyy, #zzzzzzzz"},
		{"removes a linked element", "aaaaaaaa", `<p>a, no span</p>`, "#cccccccc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, path := newPage(t, orig)
			s := open(t, home, path)
			_, _, err := s.Apply(1, &Op{Kind: "edit", ID: tc.id, HTML: tc.html})
			if !errors.Is(err, ErrDanglingLinks) || !strings.HasSuffix(err.Error(), ": "+tc.dangling) {
				t.Fatalf("err = %v, want ErrDanglingLinks naming %s", err, tc.dangling)
			}
			if got := onDisk(t, path); got != orig {
				t.Errorf("refused edit changed the file:\n%s", got)
			}
		})
	}
	home, path := newPage(t, orig)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "edit", ID: "bbbbbbbb", HTML: `<p>see <a href="#cccccccc">the code</a> and <a href="#gone0000">old</a></p>`})
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p>a <span data-kapi-id="cccccccc">y</span> <a href="#bbbbbbbb">b</a></p>`})
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
		if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: html}); err == nil {
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
		if _, _, err := s.Apply(1, &Op{Kind: "delete", ID: id}); !errors.Is(err, ErrInboundLinks) {
			t.Fatalf("delete #%s: err = %v, want ErrInboundLinks", id, err)
		}
	}
	mustApply(t, s, &Op{Kind: "delete", ID: "cccccccc"})
	mustApply(t, s, &Op{Kind: "delete", ID: "aaaaaaaa"})
	if got := onDisk(t, path); got != "" {
		t.Errorf("file after deletes = %q, want empty", got)
	}
}

func TestArchiveSetsAndUnarchiveClearsTheState(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa" class="x">a</p><kapi-step data-kapi-id="bbbbbbbb" data-kapi-state="archived" class=y>b</kapi-step><p data-kapi-id="cccccccc" data-kapi-state>c</p>`)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	mustApply(t, s, &Op{Kind: "unarchive", ID: "bbbbbbbb"})
	sum := mustApply(t, s, &Op{Kind: "unarchive", ID: "cccccccc"})
	want := `<p data-kapi-state="archived" data-kapi-id="aaaaaaaa" class="x">a</p><kapi-step data-kapi-id="bbbbbbbb" class=y>b</kapi-step><p data-kapi-id="cccccccc">c</p>`
	if got := onDisk(t, path); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if sum.Text != `unarchived "c"` || strings.Join(sum.IDs, ",") != "cccccccc" {
		t.Errorf("summary = %+v", sum)
	}
	if rev, sum, err := s.Apply(4, &Op{Kind: "unarchive", ID: "bbbbbbbb"}); err != nil || rev != 4 || sum.Text != "" {
		t.Errorf("unarchive of a block that is not archived: rev %d, %+v, %v; want no change", rev, sum, err)
	}
	if _, _, err := s.Apply(4, &Op{Kind: "done", ID: "aaaaaaaa"}); err == nil || !strings.Contains(err.Error(), `unknown op kind "done"`) {
		t.Errorf("done: err = %v, want an unknown op kind", err)
	}
}

func TestArchiveAndUnarchiveSectionChangesEveryBlockUnderTheHeading(t *testing.T) {
	home, path := newPage(t, `<h2 data-kapi-id="aaaaaaaa">1. First</h2>
<p data-kapi-id="bbbbbbbb">one</p>
<h3 data-kapi-id="cccccccc">Detail</h3>
<p data-kapi-id="dddddddd" data-kapi-state="archived">already archived</p>
<h2 data-kapi-id="eeeeeeee">2. Second</h2>
<p data-kapi-id="ffffffff">two</p>
`)
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa", Section: true})
	if got := strings.Join(sum.IDs, ","); got != "aaaaaaaa,bbbbbbbb,cccccccc" {
		t.Fatalf("archived ids = %s", got)
	}
	want := `<h2 data-kapi-state="archived" data-kapi-archive-section="" data-kapi-id="aaaaaaaa">1. First</h2>
<p data-kapi-state="archived" data-kapi-archived-with="aaaaaaaa" data-kapi-id="bbbbbbbb">one</p>
<h3 data-kapi-state="archived" data-kapi-archived-with="aaaaaaaa" data-kapi-id="cccccccc">Detail</h3>
<p data-kapi-id="dddddddd" data-kapi-state="archived">already archived</p>
<h2 data-kapi-id="eeeeeeee">2. Second</h2>
<p data-kapi-id="ffffffff">two</p>
`
	if got := onDisk(t, path); got != want {
		t.Fatalf("archived section:\n%s\nwant:\n%s", got, want)
	}
	sum = mustApply(t, s, &Op{Kind: "unarchive", ID: "aaaaaaaa", Section: true})
	if got := strings.Join(sum.IDs, ","); got != "aaaaaaaa,bbbbbbbb,cccccccc" {
		t.Fatalf("unarchived ids = %s", got)
	}
	want = `<h2 data-kapi-id="aaaaaaaa">1. First</h2>
<p data-kapi-id="bbbbbbbb">one</p>
<h3 data-kapi-id="cccccccc">Detail</h3>
<p data-kapi-id="dddddddd" data-kapi-state="archived">already archived</p>
<h2 data-kapi-id="eeeeeeee">2. Second</h2>
<p data-kapi-id="ffffffff">two</p>
`
	if got := onDisk(t, path); got != want {
		t.Fatalf("unarchived section:\n%s\nwant:\n%s", got, want)
	}
}

func TestDeleteSectionRemovesTheHeadingAndEveryBlockFoldedUnderIt(t *testing.T) {
	original := `<h2 data-kapi-id="aaaaaaaa">1. First</h2>
<p data-kapi-id="bbbbbbbb">one</p>
<h3 data-kapi-id="cccccccc">Detail</h3>
<p data-kapi-id="dddddddd">more</p>
<h2 data-kapi-id="eeeeeeee">2. Second</h2>
<p data-kapi-id="ffffffff">two</p>
`
	home, path := newPage(t, original)
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "delete", ID: "aaaaaaaa", Section: true})
	if got := strings.Join(sum.IDs, ","); got != "aaaaaaaa,bbbbbbbb,cccccccc,dddddddd" {
		t.Fatalf("deleted ids = %s", got)
	}
	want := `<h2 data-kapi-id="eeeeeeee">2. Second</h2>
<p data-kapi-id="ffffffff">two</p>
`
	if got := onDisk(t, path); got != want {
		t.Fatalf("after section delete:\n%s\nwant:\n%s", got, want)
	}
	mustApply(t, s, &Op{Kind: "undo"})
	if got := onDisk(t, path); got != original {
		t.Fatalf("undo section delete:\n%s\nwant:\n%s", got, original)
	}
}

func TestDeleteSectionChecksEveryChildForStalenessAndInboundLinks(t *testing.T) {
	home, path := newPage(t, `<h2 data-kapi-id="aaaaaaaa">First</h2>
<p data-kapi-id="bbbbbbbb">one</p>
<h2 data-kapi-id="cccccccc">Second</h2>
<p data-kapi-id="dddddddd"><a href="#bbbbbbbb">back</a></p>
`)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "delete", ID: "aaaaaaaa", Section: true}); !errors.Is(err, ErrInboundLinks) {
		t.Fatalf("section delete with inbound link: %v, want ErrInboundLinks", err)
	}
	inner := "changed"
	mustApply(t, s, &Op{Kind: "edit", ID: "bbbbbbbb", Inner: &inner})
	if _, _, err := s.Apply(1, &Op{Kind: "delete", ID: "aaaaaaaa", Section: true}); !errors.Is(err, ErrStale) {
		t.Fatalf("section delete after a child changed: %v, want ErrStale", err)
	}
}

func TestSectionArchiveAtAnOldRevisionChecksTheWholeSection(t *testing.T) {
	home, path := newPage(t, `<h2 data-kapi-id="aaaaaaaa">First</h2>
<p data-kapi-id="bbbbbbbb">one</p>
<h2 data-kapi-id="cccccccc">Second</h2>
`)
	s := open(t, home, path)
	inner := "changed"
	mustApply(t, s, &Op{Kind: "edit", ID: "bbbbbbbb", Inner: &inner})
	if _, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa", Section: true}); !errors.Is(err, ErrStale) {
		t.Fatalf("section archive after a child changed: %v, want ErrStale", err)
	}
}

func TestAChildRearchivedSeparatelyStaysArchivedWithItsSectionRestored(t *testing.T) {
	home, path := newPage(t, `<h2 data-kapi-id="aaaaaaaa">First</h2>
<p data-kapi-id="bbbbbbbb">one</p>
<h2 data-kapi-id="cccccccc">Second</h2>
`)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa", Section: true})
	mustApply(t, s, &Op{Kind: "unarchive", ID: "bbbbbbbb"})
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})
	mustApply(t, s, &Op{Kind: "unarchive", ID: "aaaaaaaa", Section: true})
	want := `<h2 data-kapi-id="aaaaaaaa">First</h2>
<p data-kapi-state="archived" data-kapi-id="bbbbbbbb">one</p>
<h2 data-kapi-id="cccccccc">Second</h2>
`
	if got := onDisk(t, path); got != want {
		t.Fatalf("restored section:\n%s\nwant:\n%s", got, want)
	}
}

func TestSectionUnarchiveRestoresAnOwnedBlockMovedOutsideTheSection(t *testing.T) {
	home, path := newPage(t, `<h2 data-kapi-id="aaaaaaaa">First</h2>
<p data-kapi-id="bbbbbbbb">one</p>
<h2 data-kapi-id="cccccccc">Second</h2>
`)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa", Section: true})
	mustApply(t, s, &Op{Kind: "move", ID: "bbbbbbbb"})
	mustApply(t, s, &Op{Kind: "unarchive", ID: "aaaaaaaa", Section: true})
	want := `<h2 data-kapi-id="aaaaaaaa">First</h2>
<h2 data-kapi-id="cccccccc">Second</h2>
<p data-kapi-id="bbbbbbbb">one</p>
`
	if got := onDisk(t, path); got != want {
		t.Fatalf("restored moved child:\n%s\nwant:\n%s", got, want)
	}
}

func TestSectionArchiveNeedsAHeading(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">not a heading</p>`)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa", Section: true}); err == nil || !strings.Contains(err.Error(), "needs a heading") {
		t.Fatalf("section archive error = %v", err)
	}
}

func TestUnarchiveAtAnOldRevChecksOnlyItsBlock(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\" data-kapi-state=\"archived\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})
	if _, _, err := s.Apply(1, &Op{Kind: "unarchive", ID: "aaaaaaaa"}); err != nil {
		t.Fatalf("unarchive of an unchanged block at an old rev: %v", err)
	}
	if _, _, err := s.Apply(1, &Op{Kind: "unarchive", ID: "bbbbbbbb"}); !errors.Is(err, ErrStale) {
		t.Errorf("unarchive of a block that changed since that rev: err = %v, want ErrStale", err)
	}
	if _, _, err := s.Apply(2, &Op{Kind: "unarchive", ID: "bbbbbbbb"}); err != nil {
		t.Fatalf("unarchive at an old rev of a block unchanged since: %v", err)
	}
}

func TestMoveBeforeAndToTheEnd(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n<kapi-plan data-kapi-id=\"cccccccc\">\n  <kapi-step data-kapi-id=\"dddddddd\">d</kapi-step>\n  <kapi-step data-kapi-id=\"eeeeeeee\">e</kapi-step>\n</kapi-plan>\n")
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "move", ID: "bbbbbbbb", Before: "aaaaaaaa"})
	mustApply(t, s, &Op{Kind: "move", ID: "bbbbbbbb"})
	mustApply(t, s, &Op{Kind: "move", ID: "dddddddd"})
	want := "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<kapi-plan data-kapi-id=\"cccccccc\">\n  <kapi-step data-kapi-id=\"eeeeeeee\">e</kapi-step>\n  <kapi-step data-kapi-id=\"dddddddd\">d</kapi-step>\n</kapi-plan>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n"
	if got := onDisk(t, path); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if _, _, err := s.Apply(4, &Op{Kind: "move", ID: "cccccccc", Before: "eeeeeeee"}); err == nil {
		t.Error("moving an element before its own child succeeded")
	}
}

func TestUndoWalksBackThroughRevisions(t *testing.T) {
	orig := "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n"
	home, path := newPage(t, orig)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "undo"}); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo on a fresh page: err = %v", err)
	}
	mustApply(t, s, &Op{Kind: "delete", ID: "aaaaaaaa"})
	afterDelete := onDisk(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})

	mustApply(t, s, &Op{Kind: "undo"})
	if got := onDisk(t, path); got != afterDelete {
		t.Fatalf("first undo =\n%s\nwant\n%s", got, afterDelete)
	}
	mustApply(t, s, &Op{Kind: "undo"})
	if got := onDisk(t, path); got != orig {
		t.Fatalf("second undo =\n%s\nwant\n%s", got, orig)
	}
	src, rev := open(t, home, path).Source()
	if rev != 5 || string(src) != orig {
		t.Errorf("after reopen rev = %d, source =\n%s", rev, src)
	}
}

func TestCanUndoOnlyWhenTheLastBrowserOpIsStillOnTop(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	s := open(t, home, path)
	if s.CanUndo() {
		t.Fatal("a fresh page has nothing to undo")
	}
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	if !s.CanUndo() {
		t.Fatal("an archive can be undone")
	}
	mustApply(t, s, &Op{Kind: "undo"})
	if s.CanUndo() {
		t.Fatal("undoing the only op leaves nothing to undo")
	}
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})
	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\" data-kapi-state=\"archived\">b, by the agent</p>\n")
	if _, changed, err := s.Refresh(); err != nil || !changed {
		t.Fatalf("Refresh: changed=%v err=%v", changed, err)
	}
	if s.CanUndo() {
		t.Error("an op buried under a file edit cannot be undone, so undo is not offered")
	}
}

func TestRefreshPicksUpAnExternalEdit(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	s := open(t, home, path)
	if _, changed, err := s.Refresh(); err != nil || changed {
		t.Fatalf("Refresh with no edit: changed=%v err=%v", changed, err)
	}

	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b, edited</p>\n")
	if _, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa"}); !errors.Is(err, ErrStale) {
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

	before := onDisk(t, path)
	if _, _, err := s.Apply(3, &Op{Kind: "undo"}); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo with only file edits: err = %v, want ErrNothingToUndo", err)
	}
	if onDisk(t, path) != before {
		t.Error("undo reverted an edit made in the file")
	}
}

func TestOpenNoticesAnEditMadeWhileClosed(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	open(t, home, path)
	externalWrite(t, path, `<p data-kapi-id="aaaaaaaa">changed</p>`)
	l := pageLog(t, home, path)
	s, err := Open(home, path, l)
	if err != nil {
		t.Fatal(err)
	}
	if _, rev := s.Source(); rev != 2 {
		t.Fatalf("rev = %d, want 2", rev)
	}
	if events, _ := l.After(0); len(events) != 1 || events[0].Author != "file" || events[0].Kind != "revision" || events[0].Changed != "aaaaaaaa" || events[0].Rev != 2 {
		t.Errorf("events = %+v, want one file revision naming aaaaaaaa", events)
	}
	if _, _, err := s.Apply(2, &Op{Kind: "undo"}); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo of an edit made while closed: err = %v, want ErrNothingToUndo", err)
	}
}

func TestUndoRefusesToDiscardAFileEditMadeAfterTheOp(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\" data-kapi-state=\"archived\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b, by the agent</p>\n")
	if _, changed, err := s.Refresh(); err != nil || !changed {
		t.Fatalf("Refresh: changed=%v err=%v", changed, err)
	}
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})

	mustApply(t, s, &Op{Kind: "undo"})
	agents := onDisk(t, path)
	if !strings.Contains(agents, "b, by the agent") || strings.Count(agents, "archived") != 1 {
		t.Fatalf("undo of the op after the file edit =\n%s", agents)
	}
	_, rev := s.Source()
	if _, _, err := s.Apply(rev, &Op{Kind: "undo"}); !errors.Is(err, ErrUndoConflict) {
		t.Fatalf("undo across a file edit: err = %v, want ErrUndoConflict", err)
	}
	if onDisk(t, path) != agents {
		t.Errorf("refused undo changed the file:\n%s", onDisk(t, path))
	}
}

func TestApplyUnknownIDOrKind(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "delete", ID: "zzzzzzzz"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Apply(1, &Op{Kind: "explode", ID: "aaaaaaaa"}); err == nil {
		t.Error("unknown kind succeeded")
	}
}

func TestOpenNeedsAnAbsoluteExistingPath(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing.html")
	if _, err := Open(home, "plan.html", pageLog(t, home, missing)); err == nil {
		t.Error("relative path accepted")
	}
	if _, err := Open(home, missing, pageLog(t, home, missing)); !errors.Is(err, os.ErrNotExist) {
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

const hoistRepro = `<p data-kapi-id="aaaaaaaa">before <kapi-callout data-kapi-id="bbbbbbbb">keep me</kapi-callout> after</p>`

func renderedBlocks(t *testing.T, src []byte) []string {
	t.Helper()
	out, err := render.Render(src)
	if err != nil {
		t.Fatal(err)
	}
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(out), body)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.Type != html.ElementNode {
			continue
		}
		id := ""
		for _, a := range n.Attr {
			if a.Key == idAttr {
				id = a.Val
			}
		}
		if id == "" || seen[id] {
			t.Errorf("rendered <%s> has id %q, want a unique one:\n%s", n.Data, id, out)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func TestOpenSplitsPresetsOutOfProseInTheSource(t *testing.T) {
	home, path := newPage(t, hoistRepro+"\n")
	s := open(t, home, path)
	src, _ := s.Source()
	ids := renderedBlocks(t, src)
	if len(ids) != 3 || ids[0] != "aaaaaaaa" || ids[1] != "bbbbbbbb" {
		t.Fatalf("blocks = %v, want #aaaaaaaa, #bbbbbbbb and a fresh id:\n%s", ids, src)
	}
	if got := onDisk(t, path); got != string(src) {
		t.Errorf("split source was not written back:\n%s", got)
	}
}

func TestEditAfterASplitKeepsEverything(t *testing.T) {
	home, path := newPage(t, hoistRepro)
	s := open(t, home, path)
	src, _ := s.Source()
	block, err := render.Block(src, "aaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(block), "before", "before, edited", 1)
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: edited})
	got, _ := s.Source()
	for _, want := range []string{"before, edited", `data-kapi-id="bbbbbbbb"`, "keep me", "after"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("source lost %q after editing #aaaaaaaa:\n%s", want, got)
		}
	}
	renderedBlocks(t, got)
}

func TestOpenGivesFormattingCopiesFreshIDs(t *testing.T) {
	home, path := newPage(t, `<b data-kapi-id="aaaaaaaa">bold<div>block</b>after</div>`)
	src, _ := open(t, home, path).Source()
	renderedBlocks(t, src)
	if n := strings.Count(string(src), `"aaaaaaaa"`); n != 1 {
		t.Errorf("id kept %d times:\n%s", n, src)
	}
}

func TestEditRefusesProseThatHoldsPresets(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s := open(t, home, path)
	for _, h := range []string{
		`<p data-kapi-id="aaaaaaaa">x <kapi-callout>y</kapi-callout></p>`,
		`<div data-kapi-id="aaaaaaaa"><aside class="kapi-callout" data-kapi-preset="callout">rendered</aside></div>`,
		`<p data-kapi-id="aaaaaaaa" data-kapi-preset="code">x</p>`,
	} {
		if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: h}); !errors.Is(err, ErrPresetInProse) {
			t.Errorf("edit %q: err = %v, want ErrPresetInProse", h, err)
		}
	}
	if got := onDisk(t, path); got != `<p data-kapi-id="aaaaaaaa">a</p>` {
		t.Errorf("refused edit changed the file: %s", got)
	}
}

func TestEditKeepsBadgesInProse(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s := open(t, home, path)
	h := `<p data-kapi-id="aaaaaaaa">PR #19 is <kapi-badge tone="green">merged</kapi-badge></p>`
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: h})
	if got := onDisk(t, path); !regexp.MustCompile(`^<p data-kapi-id="aaaaaaaa">PR #19 is <kapi-badge data-kapi-id="[a-z2-7]{8}" tone="green">merged</kapi-badge></p>$`).MatchString(got) {
		t.Errorf("after the edit: %s", got)
	}
}

const lavishPage = "<!doctype html>\n<html><head><style>body{color:red}</style></head><body>\n" +
	"<h1 class=title>Report</h1>\n<p onclick=\"go()\">Hello &amp; welcome</p>\n<script>go()</script>\n</body></html>\n"

func rawSaves(t *testing.T, s *Store) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(s.dir, "raw", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestOpenOfAFullDocumentOnlyAddsIDs(t *testing.T) {
	home, path := newPage(t, lavishPage)
	s := open(t, home, path)
	got := onDisk(t, path)
	if stripped := regexp.MustCompile(` data-kapi-id="[a-z2-7]{8}"`).ReplaceAllString(got, ""); stripped != lavishPage {
		t.Fatalf("open changed more than ids:\n%s", got)
	}
	src, _ := s.Source()
	if ids := renderedBlocks(t, src); len(ids) != 2 {
		t.Errorf("want ids on the h1 and the p, got %v:\n%s", ids, got)
	}
	if s.Conversion() != "" || len(rawSaves(t, s)) != 0 {
		t.Errorf("adding ids alone reported %q and saved %v", s.Conversion(), rawSaves(t, s))
	}
}

func TestRefreshKeepsTheAgentsBytes(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	s := open(t, home, path)
	written := "<p data-kapi-id='q0000001' class=x>Fish &amp; chips &copy;</p>\n<script>later()</script>\n<p data-kapi-id='q0000002'>b</p>\n"
	externalWrite(t, path, written)
	if _, ok, err := s.Refresh(); err != nil || !ok {
		t.Fatalf("Refresh = %v, %v", ok, err)
	}
	if got := onDisk(t, path); got != written {
		t.Errorf("refresh rewrote the file:\n%s", got)
	}
	src, rev := s.Source()
	if rev != 2 || string(src) != written {
		t.Errorf("Source = rev %d %q", rev, src)
	}
	if out, _ := render.Render(src); strings.Contains(string(out), "later()") {
		t.Errorf("served output kept the script: %s", out)
	}
}

func TestSplitsSaveTheRawInputEveryTime(t *testing.T) {
	home, path := newPage(t, hoistRepro)
	s := open(t, home, path)
	if saves := rawSaves(t, s); len(saves) != 1 || !strings.Contains(s.Conversion(), "raw input saved to "+saves[0]) {
		t.Fatalf("saves %v, conversion %q", saves, s.Conversion())
	}
	if b, _ := os.ReadFile(filepath.Join(s.dir, "raw", "1.html")); string(b) != hoistRepro {
		t.Errorf("raw/1.html = %q, want the untouched input", b)
	}
	for i, extra := range []string{"<p>x <kapi-code>c</kapi-code> y<script>s()</script></p>", "<b>bold<div>block</b>after</div>"} {
		src, _ := s.Source()
		input := string(src) + "\n" + extra + "\n"
		externalWrite(t, path, input)
		sum, ok, err := s.Refresh()
		if err != nil || !ok {
			t.Fatalf("Refresh = %v, %v", ok, err)
		}
		_, rev := s.Source()
		saved := filepath.Join(s.dir, "raw", strconv.FormatUint(rev, 10)+".html")
		if b, err := os.ReadFile(saved); err != nil || string(b) != input {
			t.Errorf("edit %d: %s = %q, %v; want the input", i, saved, b, err)
		}
		if !strings.Contains(sum.Text, "raw input saved to "+saved) {
			t.Errorf("edit %d: summary %q does not name %s", i, sum.Text, saved)
		}
		got, _ := s.Source()
		renderedBlocks(t, got)
	}
	if len(rawSaves(t, s)) != 3 {
		t.Errorf("raw saves = %v, want one per split write", rawSaves(t, s))
	}
}

func TestOpenProcessesAPageFromBeforeTheStore(t *testing.T) {
	home, path := newPage(t, hoistRepro)
	dir := filepath.Join(home, "pages", ID(path))
	if err := os.MkdirAll(filepath.Join(dir, "revs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "revs", "1.html"), []byte(hoistRepro), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"rev":1,"undo":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, home, path)
	src, rev := s.Source()
	if rev != 2 || !strings.Contains(s.Conversion(), "split 1 block") {
		t.Errorf("rev %d, conversion %q", rev, s.Conversion())
	}
	renderedBlocks(t, src)
}

func TestCodeBodiesStayUntouchedInTheFile(t *testing.T) {
	body := "\nstd::vector<int> v;\n<td data-kapi-id=\"cellcell\">x</td> <kapi-callout>no</kapi-callout>\n"
	home, path := newPage(t, `<kapi-code lang="cpp">`+body+`</kapi-code>`+"\n")
	open(t, home, path)
	got := onDisk(t, path)
	if !strings.Contains(got, ">"+body+"</kapi-code>") || strings.Count(got, "data-kapi-id") != 2 {
		t.Errorf("file after open:\n%s", got)
	}
	if _, err := Outer([]byte(got), "cellcell"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an id inside a code body is addressable")
	}
}

func TestOpsChangeOnlyTheValueTheyTouch(t *testing.T) {
	src := "<p data-kapi-id='aaaaaaaa' class=lead data-kapi-state=done>x</p>\n<p data-kapi-id=\"bbbbbbbb\" hidden>y</p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	want := "<p data-kapi-id='aaaaaaaa' class=lead data-kapi-state=\"archived\">x</p>\n<p data-kapi-id=\"bbbbbbbb\" hidden>y</p>\n"
	if got := onDisk(t, path); got != want {
		t.Errorf("after archive:\n%s\nwant:\n%s", got, want)
	}
}

func TestOpenLabelsBlocksTheParserInvents(t *testing.T) {
	raw := `<p data-kapi-id="aaaaaaaa">a<div data-kapi-id="bbbbbbbb">b</div></p>` + "\n"
	home, path := newPage(t, raw)
	s := open(t, home, path)
	src, _ := s.Source()
	if ids := renderedBlocks(t, src); len(ids) != 3 {
		t.Errorf("blocks = %v:\n%s", ids, src)
	}
	if !strings.Contains(s.Conversion(), "raw input saved to") {
		t.Errorf("conversion = %q", s.Conversion())
	}
}

func TestAnAgentWritingInALoopAlwaysWins(t *testing.T) {
	for trial := 0; trial < 30; trial++ {
		home, path := newPage(t, "<p data-kapi-id='q0000001'>start</p>\n")
		s := open(t, home, path)
		version := func(i int) string {
			return fmt.Sprintf("<p data-kapi-id='q0000001'>v%d</p>\n<p>needs an id %d</p>\n", i, i)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < 40; i++ {
				if err := os.WriteFile(path, []byte(version(i)), 0o600); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	poll:
		for {
			select {
			case <-done:
				break poll
			default:
				if _, _, err := s.Refresh(); err != nil {
					t.Fatal(err)
				}
			}
		}
		for i := 0; i < 10; i++ {
			if _, changed, err := s.Refresh(); err != nil || !changed {
				break
			}
		}
		got := onDisk(t, path)
		if stripped := regexp.MustCompile(` data-kapi-id="[a-z2-7]{8}"`).ReplaceAllString(got, ""); stripped != version(39) {
			t.Fatalf("trial %d: the agent's last write was lost:\n%s", trial, got)
		}
	}
}

func TestEditRefusesAResultThatLosesItsID(t *testing.T) {
	src := `<p data-kapi-id="aaaaaaaa">a</p>` + "\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	for _, h := range []string{
		`<td data-kapi-id="aaaaaaaa">cell</td>`,
		`<tr data-kapi-id="aaaaaaaa"><td>x</td></tr>`,
		`<script data-kapi-id="aaaaaaaa">x()</script>`,
	} {
		if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: h}); !errors.Is(err, ErrEditLostID) {
			t.Errorf("edit %q: err = %v, want ErrEditLostID", h, err)
		}
	}
	if got := onDisk(t, path); got != src {
		t.Errorf("refused edit changed the file: %s", got)
	}
}

func TestAPastedDuplicateKeepsTheIDOnTheOriginal(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">A</p>\n<p data-kapi-id=\"xxxxxxxx\">original</p>\n<p data-kapi-id=\"bbbbbbbb\">B</p>\n")
	s := open(t, home, path)
	externalWrite(t, path, "<p data-kapi-id=\"xxxxxxxx\">pasted copy</p>\n<p data-kapi-id=\"aaaaaaaa\">A</p>\n<p data-kapi-id=\"xxxxxxxx\">original</p>\n<p data-kapi-id=\"bbbbbbbb\">B</p>\n")
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	src, _ := s.Source()
	if b, err := Outer(src, "xxxxxxxx"); err != nil || string(b) != `<p data-kapi-id="xxxxxxxx">original</p>` {
		t.Errorf("#xxxxxxxx is now %q, %v; want the original block\n%s", b, err, src)
	}
	renderedBlocks(t, src)
}

func TestOpsNeverOverwriteAnAgentsAtomicRename(t *testing.T) {
	orig := syncFile
	syncFile = func(*os.File) error { return nil }
	defer func() { syncFile = orig }()
	lost := 0
	for trial := 0; trial < 400; trial++ {
		home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">start</p>\n")
		s := open(t, home, path)
		final := fmt.Sprintf("<p data-kapi-id=\"aaaaaaaa\">agent %d</p>\n", trial)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < 30; i++ {
				body := fmt.Sprintf("<p data-kapi-id=\"aaaaaaaa\">draft %d</p>\n", i)
				if i == 29 {
					body = final
				}
				tmp := path + ".agent"
				if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
					t.Error(err)
					return
				}
				if err := os.Rename(tmp, path); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		kinds := []string{"archive", "unarchive"}
	ops:
		for k := 0; ; k++ {
			select {
			case <-done:
				break ops
			default:
			}
			_, rev := s.Source()
			if _, _, err := s.Apply(rev, &Op{Kind: kinds[k%2], ID: "aaaaaaaa"}); errors.Is(err, ErrStale) {
				if _, _, err := s.Refresh(); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
		if !strings.Contains(onDisk(t, path), fmt.Sprintf(">agent %d<", trial)) {
			lost++
		}
	}
	if lost > 0 {
		t.Errorf("ops overwrote the agent's final write in %d of 400 trials", lost)
	}
}

func agentRename(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".agent"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func TestAnAgentRenameInsideTheWriteWindowWins(t *testing.T) {
	for _, stages := range [][]string{{"swap"}, {"swap", "restore"}, {"swap", "restore", "restore"}} {
		home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">start</p>\n")
		s := open(t, home, path)
		var last string
		n := 0
		replaceHook = func(stage string) {
			if n < len(stages) && stage == stages[n] {
				last = fmt.Sprintf("<p data-kapi-id=\"aaaaaaaa\">agent %d</p>\n", n)
				agentRename(t, path, last)
				n++
			}
		}
		_, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa"})
		replaceHook = nil
		if !errors.Is(err, ErrStale) {
			t.Errorf("%v: Apply err = %v, want ErrStale", stages, err)
		}
		if got := onDisk(t, path); got != last {
			t.Errorf("%v: file = %q, want the agent's last write %q", stages, got, last)
		}
	}
}

func TestTheLastAgentRenameWinsWhenItsMtimeTiesWithTheOneBefore(t *testing.T) {
	tick := time.Now().Truncate(time.Second)
	for _, stages := range [][]string{{"swap", "restore"}, {"swap", "restore", "restore"}} {
		home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">start</p>\n")
		s := open(t, home, path)
		var last string
		n := 0
		replaceHook = func(stage string) {
			if n < len(stages) && stage == stages[n] {
				last = fmt.Sprintf("<p data-kapi-id=\"aaaaaaaa\">agent %d</p>\n", n)
				agentRename(t, path, last)
				if err := os.Chtimes(path, tick, tick); err != nil {
					t.Fatal(err)
				}
				n++
			}
		}
		_, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa"})
		replaceHook = nil
		if !errors.Is(err, ErrStale) {
			t.Errorf("%v: Apply err = %v, want ErrStale", stages, err)
		}
		if got := onDisk(t, path); got != last {
			t.Errorf("%v: file = %q, want the agent's last write %q", stages, got, last)
		}
	}
}

func TestAnAgentWriteIntoTheFileWinsWhenItsMtimeTiesWithTheOneBefore(t *testing.T) {
	tick := time.Now().Truncate(time.Second)
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">start</p>\n")
	s := open(t, home, path)
	last := "<p data-kapi-id=\"aaaaaaaa\">agent 1</p>\n"
	replaceHook = func(stage string) {
		switch stage {
		case "swap":
			agentRename(t, path, "<p data-kapi-id=\"aaaaaaaa\">agent 0</p>\n")
		case "restore":
			if err := os.WriteFile(path, []byte(last), 0o600); err != nil {
				t.Fatal(err)
			}
			replaceHook = nil
		}
		if err := os.Chtimes(path, tick, tick); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	if !errors.Is(err, ErrStale) {
		t.Errorf("Apply err = %v, want ErrStale", err)
	}
	if got := onDisk(t, path); got != last {
		t.Errorf("file = %q, want the agent's last write %q", got, last)
	}
}

func lateInPlaceWrite(t *testing.T, holdAt string, gap time.Duration, body func(string) string) {
	t.Helper()
	start := "<p data-kapi-id=\"aaaaaaaa\">start</p>\n"
	home, path := newPage(t, start)
	s := open(t, home, path)
	var held *os.File
	replaceHook = func(stage string) {
		if holdAt == "restore" && stage == "swap" {
			agentRename(t, path, "<p data-kapi-id=\"aaaaaaaa\">agent</p>\n")
		}
		if stage == holdAt && held == nil {
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			held = f
		}
	}
	_, _, err := s.Apply(1, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	if held == nil {
		t.Fatalf("held at %s: the agent never opened the file (Apply err %v)", holdAt, err)
	}
	time.Sleep(gap)
	last := body(start)
	if err := held.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := held.WriteAt([]byte(last), 0); err != nil {
		t.Fatal(err)
	}
	held.Close()
	tick(t, s, 1)
	if got := onDisk(t, path); got != last {
		t.Errorf("held at %s, wrote %v later: file = %q after a refresh, want the agent's write %q", holdAt, gap, got, last)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-*")); len(left) > 0 {
		t.Errorf("held at %s: files left next to the page: %v", holdAt, left)
	}
}

func TestALateWriteIntoTheFileKapiMovedAsideWinsAtTheNextRefresh(t *testing.T) {
	late := func(string) string { return "<p data-kapi-id=\"aaaaaaaa\">late</p>\n" }
	for _, holdAt := range []string{"swap", "restore"} {
		for _, gap := range []time.Duration{0, 5 * time.Millisecond, 20 * time.Millisecond} {
			lateInPlaceWrite(t, holdAt, gap, late)
		}
	}
}

func TestOpenSettlesAFileKapiMovedAsideBeforeItStopped(t *testing.T) {
	for _, write := range []bool{true, false} {
		start := "<p data-kapi-id=\"aaaaaaaa\">start</p>\n"
		late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
		home, path := newPage(t, start)
		s := open(t, home, path)
		var held *os.File
		replaceHook = func(stage string) {
			if stage == "swap" && held == nil {
				f, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				held = f
			}
		}
		mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
		replaceHook = nil
		want := onDisk(t, path)
		if write {
			if err := held.Truncate(0); err != nil {
				t.Fatal(err)
			}
			if _, err := held.WriteAt([]byte(late), 0); err != nil {
				t.Fatal(err)
			}
			want = late
		}
		held.Close()
		open(t, home, path)
		if got := onDisk(t, path); got != want {
			t.Errorf("late write %v: after a restart the page is %q, want %q", write, got, want)
		}
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-*")); len(left) > 0 {
			t.Errorf("late write %v: files left after a restart: %v", write, left)
		}
	}
}

func TestAnAgentRewritingIdenticalBytesIntoTheDisplacedFileStillWins(t *testing.T) {
	lateInPlaceWrite(t, "swap", 10*time.Millisecond, func(start string) string { return start })
}

func TestOpenUnderAnAgentThatKeepsWritingStillOpens(t *testing.T) {
	fails := 0
	for trial := 0; trial < 20; trial++ {
		home, path := newPage(t, "<p>needs an id</p>\n")
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				body := []byte("<p>needs an id " + string(rune('a'+i%26)) + "</p>\n")
				_ = os.WriteFile(path, body, 0o600)
				time.Sleep(time.Millisecond)
			}
		}()
		_, err := Open(home, path, pageLog(t, home, path))
		close(stop)
		<-done
		if errors.Is(err, errFileChanged) {
			fails++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if fails > 0 {
		t.Errorf("Open failed with errFileChanged in %d of 20 trials", fails)
	}
}

func TestSplittingKeepsCodeBodyBytes(t *testing.T) {
	body := "a\r\nb\x00c"
	home, path := newPage(t, "<p>x <kapi-code>"+body+"</kapi-code> y</p>\n")
	open(t, home, path)
	if got := onDisk(t, path); !strings.Contains(got, ">"+body+"</kapi-code>") {
		t.Errorf("split changed the code body:\n%q", got)
	}
}

func TestDuplicateIDsFollowThePreviousRevision(t *testing.T) {
	cases := []struct {
		name, before, after string
		want                map[string]string
	}{
		{
			"nested copy in a blockquote",
			`<p data-kapi-id="xxxxxxxx">text</p>` + "\n",
			`<blockquote><p data-kapi-id="xxxxxxxx">quoted copy</p></blockquote>` + "\n" + `<p data-kapi-id="xxxxxxxx">text</p>` + "\n",
			map[string]string{"xxxxxxxx": `<p data-kapi-id="xxxxxxxx">text</p>`},
		},
		{
			"pasted run after the originals",
			"<p data-kapi-id=\"aaaaaaaa\">A</p>\n<p data-kapi-id=\"bbbbbbbb\">B</p>\n<p data-kapi-id=\"cccccccc\">C</p>\n<p data-kapi-id=\"dddddddd\">D</p>\n",
			"<p data-kapi-id=\"aaaaaaaa\">A</p>\n<p data-kapi-id=\"bbbbbbbb\">B</p>\n<p data-kapi-id=\"aaaaaaaa\">A copy</p>\n<p data-kapi-id=\"bbbbbbbb\">B copy</p>\n<p data-kapi-id=\"cccccccc\">C</p>\n<p data-kapi-id=\"dddddddd\">D</p>\n",
			map[string]string{"aaaaaaaa": `<p data-kapi-id="aaaaaaaa">A</p>`, "bbbbbbbb": `<p data-kapi-id="bbbbbbbb">B</p>`},
		},
	}
	for _, tc := range cases {
		home, path := newPage(t, tc.before)
		s := open(t, home, path)
		externalWrite(t, path, tc.after)
		if _, _, err := s.Refresh(); err != nil {
			t.Fatal(err)
		}
		src, _ := s.Source()
		for id, want := range tc.want {
			if got, err := Outer(src, id); err != nil || string(got) != want {
				t.Errorf("%s: #%s = %q, %v; want %q\n%s", tc.name, id, got, err, want, src)
			}
			out, _ := render.Render(src)
			if n := strings.Count(string(out), `data-kapi-id="`+id+`"`); n != 1 {
				t.Errorf("%s: rendered page has #%s %d times", tc.name, id, n)
			}
		}
		renderedBlocks(t, src)
	}
}

func TestAnEditThatSplitsSavesItsInput(t *testing.T) {
	home, path := newPage(t, `<p data-kapi-id="aaaaaaaa">a</p>`+"\n")
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<table data-kapi-id="aaaaaaaa"><p>stray</p></table>`})
	_, rev := s.Source()
	saved := filepath.Join(s.dir, "raw", strconv.FormatUint(rev, 10)+".html")
	if !strings.Contains(sum.Text, "raw input saved to "+saved) {
		t.Errorf("summary = %q, want it to name %s", sum.Text, saved)
	}
	if b, err := os.ReadFile(saved); err != nil || !strings.Contains(string(b), `<table data-kapi-id="aaaaaaaa"><p>stray</p></table>`) {
		t.Errorf("saved input = %q, %v", b, err)
	}
}

func TestAValuelessIDGetsAWellFormedValue(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id class=\"x\">hi</p>\n")
	open(t, home, path)
	if got := onDisk(t, path); !regexp.MustCompile(`^<p data-kapi-id="[a-z2-7]{8}" class="x">hi</p>\n$`).MatchString(got) {
		t.Errorf("file = %q", got)
	}
}

func ptr(s string) *string { return &s }

const innerPage = "<kapi-callout data-kapi-id=\"aaaaaaaa\"  kind='warning'>old <em>body</em></kapi-callout>\n" +
	"<kapi-code data-kapi-id=\"bbbbbbbb\" lang=\"go\">if a < b && c {\n</kapi-code>\n" +
	"<kapi-diff data-kapi-id=\"cccccccc\">-<p>\n+</kapi-callout>\n</kapi-diff>\n" +
	"<div data-kapi-id=\"dddddddd\"><p>see <a href=\"#aaaaaaaa\">it</a></p></div>\n" +
	"<table data-kapi-id=\"eeeeeeee\"><tr><td>x</td></tr></table>\n"

func TestInnerIsTheBodyByteForByte(t *testing.T) {
	for id, want := range map[string]string{
		"aaaaaaaa": "old <em>body</em>",
		"bbbbbbbb": "if a < b && c {\n",
		"cccccccc": "-<p>\n+</kapi-callout>\n",
		"dddddddd": `<p>see <a href="#aaaaaaaa">it</a></p>`,
	} {
		got, err := Inner([]byte(innerPage), id)
		if err != nil || string(got) != want {
			t.Errorf("Inner(#%s) = %q, %v; want %q", id, got, err, want)
		}
	}
	if _, err := Inner([]byte(innerPage), "zzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestAnInnerEditKeepsTheTagsAndReplacesOnlyTheBody(t *testing.T) {
	home, path := newPage(t, innerPage)
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", Inner: ptr("new <strong>text</strong>")})
	mustApply(t, s, &Op{Kind: "edit", ID: "bbbbbbbb", Inner: ptr("if x < y {\n\treturn\n}\n")})
	want := strings.Replace(innerPage, "old <em>body</em>", "new <strong>text</strong>", 1)
	want = strings.Replace(want, "if a < b && c {\n", "if x < y {\n\treturn\n}\n", 1)
	if got := onDisk(t, path); got != want {
		t.Errorf("file after inner edits:\n%s\nwant:\n%s", got, want)
	}
	if sum.Text != `edited callout "new text"` || len(sum.IDs) != 1 || sum.IDs[0] != "aaaaaaaa" {
		t.Errorf("summary = %+v", sum)
	}
}

func TestAnInnerEditFollowsTheEditRules(t *testing.T) {
	for _, tc := range []struct {
		name  string
		op    Op
		check func(error) bool
	}{
		{"closes the element early", Op{ID: "aaaaaaaa", Inner: ptr(`x</kapi-callout><kapi-callout kind="note">y`)}, func(err error) bool { return errors.Is(err, ErrInnerEscapes) }},
		{"adds a stray end tag", Op{ID: "aaaaaaaa", Inner: ptr(`x</kapi-callout>`)}, func(err error) bool { return errors.Is(err, ErrInnerEscapes) }},
		{"leaves a same-named element open", Op{ID: "aaaaaaaa", Inner: ptr(`<kapi-callout kind="note">y`)}, func(err error) bool { return errors.Is(err, ErrInnerEscapes) }},
		{"closes a raw body early", Op{ID: "bbbbbbbb", Inner: ptr("x</kapi-code><p>y</p><kapi-code>")}, func(err error) bool { return errors.Is(err, ErrInnerEscapes) }},
		{"links to a missing id", Op{ID: "aaaaaaaa", Inner: ptr(`<a href="#zzzzzzzz">z</a>`)}, func(err error) bool { return errors.Is(err, ErrDanglingLinks) }},
		{"puts a preset in prose", Op{ID: "dddddddd", Inner: ptr(`<kapi-callout kind="note">y</kapi-callout>`)}, func(err error) bool { return errors.Is(err, ErrPresetInProse) }},
		{"carries html too", Op{ID: "aaaaaaaa", HTML: "<p>x</p>", Inner: ptr("y")}, func(err error) bool { return err != nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, path := newPage(t, innerPage)
			s := open(t, home, path)
			tc.op.Kind = "edit"
			if _, _, err := s.Apply(1, &tc.op); !tc.check(err) {
				t.Fatalf("err = %v", err)
			}
			if got := onDisk(t, path); got != innerPage {
				t.Errorf("refused edit changed the file:\n%s", got)
			}
		})
	}
}

func TestAnInnerEditAtAnOldRevChecksOnlyItsBlock(t *testing.T) {
	home, path := newPage(t, innerPage)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "dddddddd"})
	if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", Inner: ptr("fresh")}); err != nil {
		t.Fatalf("inner edit of an unchanged block at an old rev: %v", err)
	}
	if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", Inner: ptr("again")}); !errors.Is(err, ErrStale) {
		t.Errorf("inner edit of a changed block at an old rev: err = %v, want ErrStale", err)
	}
}

func TestAnInnerEditThatNeedsASplitSavesTheRawInput(t *testing.T) {
	home, path := newPage(t, innerPage)
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "edit", ID: "eeeeeeee", Inner: ptr("<p>stray</p>")})
	if !strings.Contains(sum.Text, "raw input saved to") || len(rawSaves(t, s)) != 1 {
		t.Errorf("summary %q, raw saves %v", sum.Text, rawSaves(t, s))
	}
}

func TestRevisionSummariesQuoteTheBlock(t *testing.T) {
	src := "<p data-kapi-id=\"aaaaaaaa\">This page is served by Kapi itself, so every   edit you make\n  lands in the file on disk.</p>\n" +
		"<ul data-kapi-id=\"bbbbbbbb\"><li>Copy data</li><li>Flip <em>reads</em> &amp; writes</li></ul>\n" +
		"<kapi-callout data-kapi-id=\"cccccccc\" kind=\"note\">Freeze writes.</kapi-callout>\n" +
		"<kapi-code data-kapi-id=\"dddddddd\" lang=\"go\">if a<b &amp;&amp; c {\n}</kapi-code>\n" +
		"<hr data-kapi-id=\"eeeeeeee\">\n" +
		"<p data-kapi-id=\"ffffffff\">last</p>\n" +
		"<p data-kapi-id=\"gggggggg\">" + strings.Repeat("x", 70) + "</p>\n"
	for _, tc := range []struct {
		op   Op
		want string
	}{
		{Op{Kind: "delete", ID: "aaaaaaaa"}, `deleted "This page is served by Kapi itself, so every edit you make…"`},
		{Op{Kind: "delete", ID: "gggggggg"}, `deleted "` + strings.Repeat("x", 60) + "…\""},
		{Op{Kind: "archive", ID: "bbbbbbbb"}, `archived "Copy data Flip reads & writes"`},
		{Op{Kind: "archive", ID: "cccccccc"}, `archived callout "Freeze writes."`},
		{Op{Kind: "delete", ID: "dddddddd"}, `deleted code "if a<b && c { }"`},
		{Op{Kind: "delete", ID: "eeeeeeee"}, `deleted ""`},
		{Op{Kind: "move", ID: "ffffffff", Before: "aaaaaaaa"}, `moved #ffffffff "last" before #aaaaaaaa`},
		{Op{Kind: "move", ID: "aaaaaaaa"}, `moved #aaaaaaaa "This page is served by Kapi itself, so every edit you make…" to the end`},
		{Op{Kind: "edit", ID: "cccccccc", Inner: ptr("Thaw   them\n after.")}, `edited callout "Thaw them after."`},
		{Op{Kind: "edit", ID: "aaaaaaaa", HTML: "<h2>New title</h2>"}, `edited "New title"`},
	} {
		home, path := newPage(t, src)
		s := open(t, home, path)
		if sum := mustApply(t, s, &tc.op); sum.Text != tc.want || strings.Join(sum.IDs, ",") != map[bool]string{true: "", false: tc.op.ID}[tc.op.Kind == "move"] {
			t.Errorf("%s #%s: summary %+v, want text %q", tc.op.Kind, tc.op.ID, sum, tc.want)
		}
	}
}

func TestAFileEditSummaryQuotesUpToThreeChangedBlocks(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n<kapi-callout data-kapi-id=\"cccccccc\" kind=\"note\">c</kapi-callout>\n" +
		"<p data-kapi-id=\"dddddddd\">d</p>\n<p data-kapi-id=\"eeeeeeee\">e</p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	externalWrite(t, path, strings.Replace(src, ">b<", ">b2<", 1))
	sum, _, err := s.Refresh()
	if err != nil || sum.Text != `edited in the file: "b2"` || strings.Join(sum.IDs, ",") != "bbbbbbbb" {
		t.Errorf("one block: %+v, %v", sum, err)
	}
	next := src
	for _, r := range []string{">a<", ">b<", ">c<", ">e<"} {
		next = strings.Replace(next, r, r[:2]+"x<", 1)
	}
	externalWrite(t, path, next)
	sum, _, err = s.Refresh()
	if err != nil || sum.Text != `edited in the file: "ax", "bx", callout "cx", and 1 more` || strings.Join(sum.IDs, ",") != "aaaaaaaa,bbbbbbbb,cccccccc,eeeeeeee" {
		t.Errorf("four blocks: %+v, %v", sum, err)
	}
}

func TestAnOpThatChangesNothingMakesNoRevision(t *testing.T) {
	const src = "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><td>a</td></tr></table></kapi-table>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	inner, err := Inner([]byte(src), "aaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	outer, err := Outer([]byte(src), "aaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []Op{
		{Kind: "edit", ID: "aaaaaaaa", Inner: ptr(string(inner))},
		{Kind: "edit", ID: "aaaaaaaa", HTML: string(outer)},
		{Kind: "move", ID: "bbbbbbbb"},
	} {
		rev, sum, err := s.Apply(1, &op)
		if err != nil || rev != 1 || sum.Text != "" || sum.IDs != nil {
			t.Errorf("%+v: rev %d, summary %+v, err %v; want rev 1 and an empty summary", op, rev, sum, err)
		}
	}
	revs, _ := filepath.Glob(filepath.Join(s.dir, "revs", "*.html"))
	if len(revs) != 1 || onDisk(t, path) != src {
		t.Errorf("revs %v, file:\n%s", revs, onDisk(t, path))
	}
	if _, _, err := s.Apply(1, &Op{Kind: "undo"}); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo after no-op edits: err = %v, want ErrNothingToUndo", err)
	}
}

const tablePage = "<kapi-table data-kapi-id=\"aaaaaaaa\">\n  <table class=t>\n    <thead><tr><th scope=\"col\">Shard</th><th>Rows</th></tr></thead>\n" +
	"    <tbody><tr><td class=\"name\">beta</td><td>10</td></tr><tr><td>alpha<td>9</tr></tbody>\n  </table>\n</kapi-table>\n" +
	"<p data-kapi-id=\"bbbbbbbb\">b</p>\n"

func TestAnInnerEditCannotLeaveAChildOpen(t *testing.T) {
	const src = "<kapi-plan data-kapi-id=\"aaaaaaaa\"><kapi-step data-kapi-id=\"bbbbbbbb\">one</kapi-step></kapi-plan>\n" +
		"<kapi-callout data-kapi-id=\"cccccccc\" kind=\"note\"><p>x</p></kapi-callout>\n<p data-kapi-id=\"dddddddd\">next</p>\n"
	for _, op := range []Op{
		{Kind: "edit", ID: "aaaaaaaa", Inner: ptr(`<kapi-step data-kapi-id="bbbbbbbb">one</kapi-step><div>`)},
		{Kind: "edit", ID: "cccccccc", Inner: ptr(`<div><p>x`)},
		{Kind: "edit", ID: "cccccccc", Inner: ptr(`<p>x`)},
		{Kind: "edit", ID: "cccccccc", HTML: `<kapi-callout kind="note"><div>x</kapi-callout>`},
	} {
		home, path := newPage(t, src)
		s := open(t, home, path)
		if _, _, err := s.Apply(1, &op); !errors.Is(err, ErrInnerEscapes) {
			t.Errorf("%+v: err = %v, want ErrInnerEscapes", op, err)
		}
		if got := onDisk(t, path); got != src {
			t.Errorf("refused edit changed the file:\n%s", got)
		}
	}
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "edit", ID: "cccccccc", Inner: ptr(`<p>x<br></p><ul><li>a</li></ul>`)})
}

func TestAnInnerEditOfTheLastBlockCannotLeaveAChildOpen(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<kapi-callout data-kapi-id=\"cccccccc\" kind=\"note\">x</kapi-callout>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "cccccccc", Inner: ptr(`<div>x`)}); !errors.Is(err, ErrInnerEscapes) {
		t.Errorf("err = %v, want ErrInnerEscapes", err)
	}
}

func TestAnEditThatPullsInTheNextBlockIsRefused(t *testing.T) {
	const src = "<kapi-callout data-kapi-id=\"cccccccc\" kind=\"note\">x</kapi-callout>\n<p data-kapi-id=\"dddddddd\">next</p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "cccccccc", Inner: ptr(`<span><div>x</span>`)}); !errors.Is(err, ErrInnerEscapes) {
		t.Errorf("err = %v, want ErrInnerEscapes", err)
	}
	if got := onDisk(t, path); got != src {
		t.Errorf("refused edit changed the file:\n%s", got)
	}
}

func TestRevisionExcerptsCarryNoTerminalControls(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n")
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: "<p>x&#27;]0;owned&#7;&#27;[2J&#x202e;y</p>"})
	if sum.Text != `edited "x]0;owned[2Jy"` {
		t.Errorf("summary = %q", sum.Text)
	}
}

func TestAnEditMayOmitOptionalEndTags(t *testing.T) {
	const src = "<ul data-kapi-id=\"aaaaaaaa\"><li>x</li></ul>\n<dl data-kapi-id=\"bbbbbbbb\"><dt>t</dt></dl>\n" +
		"<table data-kapi-id=\"cccccccc\"><tr><td>1</td></tr></table>\n<div data-kapi-id=\"dddddddd\"><p>p</p></div>\n<p data-kapi-id=\"eeeeeeee\">next</p>\n"
	for _, op := range []Op{
		{Kind: "edit", ID: "aaaaaaaa", Inner: ptr(`<li>a<li>b`)},
		{Kind: "edit", ID: "aaaaaaaa", HTML: `<ul><li>a<li>b</ul>`},
		{Kind: "edit", ID: "bbbbbbbb", Inner: ptr(`<dt>t<dd>d<dt>u<dd>e`)},
		{Kind: "edit", ID: "cccccccc", Inner: ptr(`<thead><tr><th>h<th>i<tbody><tr><td>1<td>2<tr><td>3<td>4<tfoot><tr><td>f`)},
		{Kind: "edit", ID: "dddddddd", Inner: ptr(`<p>one<p>two`)},
	} {
		home, path := newPage(t, src)
		s := open(t, home, path)
		if _, _, err := s.Apply(1, &op); err != nil {
			t.Errorf("%+v: %v", op, err)
		}
	}
}

func TestAPresetEndTagDoesNotCloseAnOptionalEnd(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<kapi-callout data-kapi-id=\"cccccccc\" kind=\"note\">x</kapi-callout>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "cccccccc", Inner: ptr(`<p>x`)}); !errors.Is(err, ErrInnerEscapes) {
		t.Errorf("err = %v, want ErrInnerEscapes, since HTML ignores a custom element's end tag while a p is open", err)
	}
}

func TestAnInnerEditCannotLeaveAnElementOpenInsideAClosedChild(t *testing.T) {
	for _, src := range []string{
		"<div data-kapi-id=\"aaaaaaaa\">a</div>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n",
		"<p data-kapi-id=\"bbbbbbbb\">b</p>\n<div data-kapi-id=\"aaaaaaaa\">a</div>\n",
	} {
		home, path := newPage(t, src)
		s := open(t, home, path)
		if _, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", Inner: ptr(`<span><b>x</span>`)}); !errors.Is(err, ErrInnerEscapes) {
			t.Errorf("err = %v, want ErrInnerEscapes", err)
		}
		if got := onDisk(t, path); got != src {
			t.Errorf("refused edit changed the file:\n%s", got)
		}
	}
}

func TestTheTopLevelCheckCountsBlocksWithoutIDs(t *testing.T) {
	before := []byte(`<div data-kapi-id="aaaaaaaa">a</div><hr>`)
	if !sameTopLevel(before, before, "aaaaaaaa") {
		t.Error("an unchanged page differs from itself")
	}
	if sameTopLevel(before, []byte(`<div data-kapi-id="aaaaaaaa">a<hr></div>`), "aaaaaaaa") {
		t.Error("a page that lost a top-level block without an id passed")
	}
}

func TestAFileEditThatAddsABlockStillNamesTheBlocksItChanged(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<ul data-kapi-id=\"bbbbbbbb\"><li data-kapi-id=\"cccccccc\">c</li><li data-kapi-id=\"dddddddd\">d</li></ul>\n<p data-kapi-id=\"eeeeeeee\">e</p>\n"
	home, path := newPage(t, src)
	l := pageLog(t, home, path)
	s, err := Open(home, path, l)
	if err != nil {
		t.Fatal(err)
	}
	next := "<p data-kapi-id=\"ffffffff\">new</p>\n" + strings.Replace(strings.Replace(src, ">c<", ">c2<", 1), "<p data-kapi-id=\"eeeeeeee\">e</p>\n", "", 1)
	externalWrite(t, path, next)
	sum, _, err := s.Refresh()
	if err != nil || len(sum.IDs) != 0 {
		t.Fatalf("summary %+v, %v: a new block list still means no target", sum, err)
	}
	events, _ := l.After(0)
	if e := events[len(events)-1]; e.Kind != "revision" || e.Target != "" || e.Changed != "bbbbbbbb,cccccccc,eeeeeeee" {
		t.Errorf("revision = %+v, want changed bbbbbbbb,cccccccc,eeeeeeee", e)
	}
}

func TestAnImageOpAddsAFigureAfterTheTopLevelBlockOrAtTheEnd(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<ul data-kapi-id=\"bbbbbbbb\"><li data-kapi-id=\"cccccccc\">c</li></ul>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	name := "0123456789abcdef0123456789abcdef.png"
	if _, _, err := s.Apply(1, &Op{Kind: "image", ID: "aaaaaaaa", Src: name}); !errors.Is(err, ErrNoImage) {
		t.Fatalf("an image that is not stored: %v", err)
	}
	for _, bad := range []string{"../plan.html", "x.svg", ""} {
		if _, _, err := s.Apply(1, &Op{Kind: "image", ID: "aaaaaaaa", Src: bad}); !errors.Is(err, ErrNoImage) {
			t.Errorf("src %q: %v", bad, err)
		}
	}
	if err := os.MkdirAll(FilesDir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(FilesDir(path), name), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := mustApply(t, s, &Op{Kind: "image", ID: "cccccccc", Src: name})
	got := onDisk(t, path)
	want := regexp.MustCompile(`</ul>\n<figure data-kapi-id="[a-z2-7]{8}"><img src="\./plan\.files/` + name + `" alt=""></figure>\n$`)
	if !want.MatchString(got) || !strings.Contains(sum.Text, "added an image after") {
		t.Fatalf("after a nested block: %q, summary %q", got, sum.Text)
	}
	mustApply(t, s, &Op{Kind: "image", Src: name})
	if got := onDisk(t, path); !strings.HasSuffix(strings.TrimSpace(got), `alt=""></figure>`) || strings.Count(got, "<figure") != 2 {
		t.Errorf("at the end: %q", got)
	}
	mustApply(t, s, &Op{Kind: "undo"})
	mustApply(t, s, &Op{Kind: "undo"})
	if got := onDisk(t, path); got != src {
		t.Errorf("after two undos: %q, want the original", got)
	}
}

func TestImagesGoInsideTheBodyAndSurviveAColonInThePageName(t *testing.T) {
	const src = "<!doctype html>\n<html><head><title>t</title></head><body>\n<p data-kapi-id=\"aaaaaaaa\">a</p>\n</body></html>\n"
	dir := t.TempDir()
	home, path := filepath.Join(dir, "home"), filepath.Join(dir, "notes 10:30.html")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	s := open(t, home, path)
	name := "0123456789abcdef0123456789abcdef.png"
	if err := os.MkdirAll(FilesDir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(FilesDir(path), name), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := mustApply(t, s, &Op{Kind: "image", ID: "aaaaaaaa", Src: name})
	end := mustApply(t, s, &Op{Kind: "image", Src: name})
	got := onDisk(t, path)
	if i := strings.LastIndex(got, "<figure"); i < 0 || i > strings.Index(got, "</body>") {
		t.Errorf("an image landed outside the body: %q", got)
	}
	if strings.Count(got, `<img src="./notes%2010:30.files/`+name+`"`) != 2 {
		t.Errorf("src: %q", got)
	}
	for _, sum := range []Summary{after, end} {
		if len(sum.IDs) != 1 || !strings.Contains(got, `<figure data-kapi-id="`+sum.IDs[0]+`">`) {
			t.Errorf("summary %+v does not name the new figure in %q", sum, got)
		}
	}
	src2, _ := s.Source()
	out, err := render.Render(src2)
	if err != nil || strings.Count(string(out), `src="./notes%2010:30.files/`) != 2 {
		t.Errorf("rendered: %s, %v", out, err)
	}
}

func TestAnAgentsImageOpIsLoggedAsTheAgentAndCannotBeUndoneFromTheBrowser(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n")
	l := pageLog(t, home, path)
	s, err := Open(home, path, l)
	if err != nil {
		t.Fatal(err)
	}
	name := "0123456789abcdef0123456789abcdef.png"
	if err := os.MkdirAll(FilesDir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(FilesDir(path), name), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ApplyBy("agent", 1, &Op{Kind: "image", ID: "aaaaaaaa", Src: name}); err != nil {
		t.Fatal(err)
	}
	events, _ := l.After(0)
	if e := events[len(events)-1]; e.Author != "agent" || e.Kind != "revision" {
		t.Errorf("revision = %+v, want author agent", e)
	}
	if s.CanUndo() {
		t.Error("the browser could undo the agent's image")
	}
}
