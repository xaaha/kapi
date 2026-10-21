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
	mustApply(t, s, Op{Kind: "done", ID: "aaaaaaaa"})
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

	before := onDisk(t, path)
	if _, _, err := s.Apply(3, Op{Kind: "undo"}); !errors.Is(err, ErrNothingToUndo) {
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
	s := open(t, home, path)
	if _, rev := s.Source(); rev != 2 {
		t.Fatalf("rev = %d, want 2", rev)
	}
	if _, _, err := s.Apply(2, Op{Kind: "undo"}); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo of an edit made while closed: err = %v, want ErrNothingToUndo", err)
	}
}

func TestUndoRefusesToDiscardAFileEditMadeAfterTheOp(t *testing.T) {
	home, path := newPage(t, "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b</p>\n")
	s := open(t, home, path)
	mustApply(t, s, Op{Kind: "done", ID: "aaaaaaaa"})
	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\" data-kapi-state=\"done\">a</p>\n<p data-kapi-id=\"bbbbbbbb\">b, by the agent</p>\n")
	if _, changed, err := s.Refresh(); err != nil || !changed {
		t.Fatalf("Refresh: changed=%v err=%v", changed, err)
	}
	mustApply(t, s, Op{Kind: "archive", ID: "bbbbbbbb"})

	mustApply(t, s, Op{Kind: "undo"})
	agents := onDisk(t, path)
	if !strings.Contains(agents, "b, by the agent") || strings.Contains(agents, "archived") {
		t.Fatalf("undo of the op after the file edit =\n%s", agents)
	}
	_, rev := s.Source()
	if _, _, err := s.Apply(rev, Op{Kind: "undo"}); !errors.Is(err, ErrUndoConflict) {
		t.Fatalf("undo across a file edit: err = %v, want ErrUndoConflict", err)
	}
	if onDisk(t, path) != agents {
		t.Errorf("refused undo changed the file:\n%s", onDisk(t, path))
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
	mustApply(t, s, Op{Kind: "edit", ID: "aaaaaaaa", HTML: edited})
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
		if _, _, err := s.Apply(1, Op{Kind: "edit", ID: "aaaaaaaa", HTML: h}); !errors.Is(err, ErrPresetInProse) {
			t.Errorf("edit %q: err = %v, want ErrPresetInProse", h, err)
		}
	}
	if got := onDisk(t, path); got != `<p data-kapi-id="aaaaaaaa">a</p>` {
		t.Errorf("refused edit changed the file: %s", got)
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
	mustApply(t, s, Op{Kind: "archive", ID: "aaaaaaaa"})
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
		if _, _, err := s.Apply(1, Op{Kind: "edit", ID: "aaaaaaaa", HTML: h}); !errors.Is(err, ErrEditLostID) {
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
