package page

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"

	"github.com/xaaha/kapi/internal/eventlog"
)

const crashStart = "<p data-kapi-id=\"aaaaaaaa\">one</p>\n<p data-kapi-id=\"bbbbbbbb\">two</p>\n"

var crashOps = []*Op{
	{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`},
	{Kind: "archive", ID: "bbbbbbbb"},
	{Kind: "undo"},
	{Kind: "edit", ID: "bbbbbbbb", HTML: `<p data-kapi-id="bbbbbbbb">dos</p>`},
}

type writeEvent struct{ kind, from, to string }

func traceWrites(on func(writeEvent)) (restore func()) {
	sync0, rename0, swap0 := syncFile, renameFile, swapFiles
	syncFile = func(f *os.File) error { on(writeEvent{"sync", f.Name(), ""}); return sync0(f) }
	renameFile = func(a, b string) error { on(writeEvent{"rename", a, b}); return rename0(a, b) }
	swapFiles = func(a, b string) error { on(writeEvent{"swap", a, b}); return swap0(a, b) }
	return func() { syncFile, renameFile, swapFiles = sync0, rename0, swap0 }
}

func runCrashScenario(home, path string, after func(*Store)) error {
	s, err := Open(home, path)
	if err != nil {
		return err
	}
	l, err := eventlog.Open(s.dir)
	if err != nil {
		return err
	}
	defer l.Close()
	after(s)
	for _, op := range crashOps {
		_, rev := s.Source()
		_, sum, err := s.Apply(rev, op)
		if err != nil {
			return err
		}
		if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "revision", Text: sum.Text}); err != nil {
			return err
		}
		after(s)
	}
	return nil
}

func TestCrashChildProcess(t *testing.T) {
	at, err := strconv.Atoi(os.Getenv("KAPI_CRASH_AT"))
	if err != nil {
		t.Skip("runs only as the child of TestAKilledStoreRecoversAtEveryWritePoint")
	}
	n := 0
	defer traceWrites(func(writeEvent) {
		if n++; n == at {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			select {}
		}
	})()
	if err := runCrashScenario(os.Getenv("KAPI_CRASH_HOME"), os.Getenv("KAPI_CRASH_PAGE"), func(*Store) {}); err != nil {
		t.Fatal(err)
	}
}

type crashMark struct {
	writes int
	src    string
	undo   int
}

func TestAKilledStoreRecoversAtEveryWritePoint(t *testing.T) {
	home, path := newPage(t, crashStart)
	marks := []crashMark{{0, crashStart, 0}}
	n := 0
	restore := traceWrites(func(writeEvent) { n++ })
	err := runCrashScenario(home, path, func(s *Store) {
		src, _ := s.Source()
		marks = append(marks, crashMark{n, string(src), len(s.undo)})
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	whole := map[string]bool{}
	for _, m := range marks {
		whole[m.src] = true
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= n+1; at++ {
		step := 1
		for step < len(marks)-1 && marks[step].writes < at {
			step++
		}
		t.Run(strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()
			home, path := newPage(t, crashStart)
			cmd := exec.Command(exe, "-test.run=^TestCrashChildProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), "KAPI_CRASH_AT="+strconv.Itoa(at), "KAPI_CRASH_HOME="+home, "KAPI_CRASH_PAGE="+path)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			killed := errors.As(err, &exit) && exit.Sys().(syscall.WaitStatus).Signal() == syscall.SIGKILL
			if killed != (at <= n) || (!killed && err != nil) {
				t.Fatalf("child at write %d of %d: %v\n%s", at, n, err, out)
			}
			checkWhole(t, home, path, whole)
			s, err := Open(home, path)
			if err != nil {
				t.Fatalf("reopen after a kill at write %d: %v", at, err)
			}
			l, err := eventlog.Open(s.dir)
			if err != nil {
				t.Fatalf("event log after a kill at write %d: %v", at, err)
			}
			l.Close()
			for _, dir := range []string{filepath.Dir(path), s.dir, filepath.Join(s.dir, "revs")} {
				if left, _ := filepath.Glob(filepath.Join(dir, ".*.kapi-*")); len(left) > 0 {
					t.Errorf("reopen after a kill at write %d left temp files %v", at, left)
				}
			}
			src, _ := s.Source()
			got := crashMark{src: string(src), undo: len(s.undo)}
			before, after := marks[step-1], marks[step]
			if got != (crashMark{src: before.src, undo: before.undo}) && got != (crashMark{src: after.src, undo: after.undo}) {
				t.Fatalf("after a kill at write %d: source %q with %d undo steps, want the state before or after step %d", at, got.src, got.undo, step)
			}
			for {
				_, rev := s.Source()
				_, _, err := s.Apply(rev, &Op{Kind: "undo"})
				if errors.Is(err, ErrNothingToUndo) {
					break
				}
				if err != nil {
					t.Fatalf("undo after a kill at write %d: %v", at, err)
				}
			}
			if src, _ := s.Source(); string(src) != crashStart {
				t.Errorf("undoing everything after a kill at write %d left %q", at, src)
			}
		})
	}
}

func checkWhole(t *testing.T, home, path string, whole map[string]bool) {
	t.Helper()
	if got := onDisk(t, path); !whole[got] {
		t.Fatalf("page file is %q, not any version the store wrote", got)
	}
	dir := filepath.Join(home, "pages", ID(path))
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("state.json %q: %v", b, err)
	}
	revs := []uint64{st.Rev}
	for _, u := range st.Undo {
		revs = append(revs, u, u+1)
	}
	for _, r := range revs {
		b, err := os.ReadFile(filepath.Join(dir, "revs", strconv.FormatUint(r, 10)+".html"))
		if err != nil || !whole[string(b)] {
			t.Fatalf("revs/%d.html named by state %d is %q, %v", r, st.Rev, b, err)
		}
	}
}

func TestEveryWriteIsSyncedBeforeItsRenameAndItsDirectoryAfter(t *testing.T) {
	home, path := newPage(t, crashStart)
	var trace []writeEvent
	restore := traceWrites(func(e writeEvent) { trace = append(trace, e) })
	err := runCrashScenario(home, path, func(*Store) {})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	synced, renamed := map[string]bool{}, map[string]bool{}
	unsyncedDir := ""
	for i, e := range trace {
		if e.kind == "sync" {
			synced[e.from] = true
			if e.from == unsyncedDir {
				unsyncedDir = ""
			}
			continue
		}
		if e.kind == "rename" && i > 0 && trace[i-1] == (writeEvent{"swap", e.from, e.to}) {
			continue
		}
		if !synced[e.from] {
			t.Errorf("%s of %s into %s before the file was synced", e.kind, e.from, e.to)
		}
		if unsyncedDir != "" {
			t.Errorf("%s into %s before %s was synced", e.kind, e.to, unsyncedDir)
		}
		renamed[e.to] = true
		unsyncedDir = filepath.Dir(e.to)
	}
	if unsyncedDir != "" {
		t.Errorf("%s was never synced after its last rename", unsyncedDir)
	}
	dir := filepath.Join(home, "pages", ID(path))
	want := []string{path, filepath.Join(dir, "state.json")}
	for r := 1; r <= 1+len(crashOps); r++ {
		want = append(want, filepath.Join(dir, "revs", strconv.Itoa(r)+".html"))
	}
	for _, p := range want {
		if !renamed[p] {
			t.Errorf("%s was not written through a synced temp file and a rename", p)
		}
	}
}

func TestOpenRemovesOnlyItsOwnTempFiles(t *testing.T) {
	home, path := newPage(t, crashStart)
	dir := filepath.Dir(path)
	for _, name := range []string{".plan.html.kapi-123", ".other.html.kapi-123", ".plan.html.kapi-notes", "plan.html.kapi-123"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	open(t, home, path)
	left, err := filepath.Glob(filepath.Join(dir, "*kapi-*"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".other.html.kapi-123", ".plan.html.kapi-notes", "plan.html.kapi-123"}
	var got []string
	for _, p := range left {
		got = append(got, filepath.Base(p))
	}
	if !slices.Equal(got, want) {
		t.Errorf("files left %v, want %v", got, want)
	}
}
