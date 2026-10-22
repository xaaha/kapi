package page

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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

type writeEvent struct{ kind, from, to, data string }

func traceWrites(on func(writeEvent)) (restore func()) {
	sync0, rename0, swap0, write0 := syncFile, renameFile, swapFiles, writeTemp
	syncFile = func(f *os.File) error { on(writeEvent{"sync", f.Name(), "", ""}); return sync0(f) }
	renameFile = func(a, b string) error { on(writeEvent{"rename", a, b, ""}); return rename0(a, b) }
	swapFiles = func(a, b string) error { on(writeEvent{"swap", a, b, ""}); return swap0(a, b) }
	writeTemp = func(f *os.File, b []byte) (int, error) {
		on(writeEvent{"write", f.Name(), "", string(b)})
		return write0(f, b)
	}
	return func() { syncFile, renameFile, swapFiles, writeTemp = sync0, rename0, swap0, write0 }
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
	partial := os.Getenv("KAPI_CRASH_PARTIAL") != ""
	n := 0
	defer traceWrites(func(e writeEvent) {
		if n++; n == at {
			if partial {
				if err := os.WriteFile(e.from, []byte(e.data[:len(e.data)/2]), 0o600); err != nil {
					t.Fatal(err)
				}
			}
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
	var kinds []string
	restore := traceWrites(func(e writeEvent) { n++; kinds = append(kinds, e.kind) })
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
		variants := []string{""}
		if at <= n && kinds[at-1] == "write" {
			variants = append(variants, "partial")
		}
		for _, partial := range variants {
			t.Run(strconv.Itoa(at)+partial, func(t *testing.T) {
				t.Parallel()
				home, path := newPage(t, crashStart)
				cmd := exec.Command(exe, "-test.run=^TestCrashChildProcess$", "-test.count=1")
				cmd.Env = append(os.Environ(), "KAPI_CRASH_AT="+strconv.Itoa(at), "KAPI_CRASH_PARTIAL="+partial, "KAPI_CRASH_HOME="+home, "KAPI_CRASH_PAGE="+path)
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
		if e.kind == "write" {
			continue
		}
		if e.kind == "sync" {
			synced[e.from] = true
			if e.from == unsyncedDir {
				unsyncedDir = ""
			}
			continue
		}
		if e.kind == "rename" && i > 0 && trace[i-1] == (writeEvent{"swap", e.from, e.to, ""}) {
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

const (
	oneBlock    = "<p data-kapi-id=\"aaaaaaaa\">one</p>\n"
	oneBlockUno = "<p data-kapi-id=\"aaaaaaaa\">uno</p>\n"
)

func TestARevisionNeverGoesBackwardAfterARestart(t *testing.T) {
	home, path := newPage(t, oneBlock)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
	_, rev := s.Source()
	externalWrite(t, path, oneBlock)
	if _, got := open(t, home, path).Source(); got <= rev {
		t.Fatalf("after the file went back to revision 1 while kapi was down, rev = %d, want above %d", got, rev)
	}
}

func TestUndoKeepsAFileEditMadeWhileKapiWasDown(t *testing.T) {
	home, path := newPage(t, oneBlock)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
	mustApply(t, s, &Op{Kind: "undo"})
	externalWrite(t, path, oneBlockUno)
	s = open(t, home, path)
	_, rev := s.Source()
	_, _, err := s.Apply(rev, &Op{Kind: "undo"})
	if got := onDisk(t, path); err == nil || got != oneBlockUno {
		t.Fatalf("undo after a file edit made while kapi was down: err = %v, file %q; want the edit kept", err, got)
	}
}

const agentWrite = "<p>agent wrote this</p>\n"

func TestRestoreChildProcess(t *testing.T) {
	path := os.Getenv("KAPI_RESTORE_PAGE")
	if path == "" {
		t.Skip("runs only as the child of TestOpenRestoresAnAgentWriteAKillLeftInATempFile")
	}
	s := open(t, os.Getenv("KAPI_RESTORE_HOME"), path)
	replaceHook = func(stage string) {
		switch stage {
		case "swap":
			agentRename(t, path, agentWrite)
		case "restore":
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			select {}
		}
	}
	_, _, _ = s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
	t.Fatal("the restore swap never ran")
}

func killMidRestore(t *testing.T, home, path string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestRestoreChildProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "KAPI_RESTORE_HOME="+home, "KAPI_RESTORE_PAGE="+path)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("child was not killed in the restore window: %v\n%s", err, out)
	}
}

func TestOpenRestoresAnAgentWriteAKillLeftInATempFile(t *testing.T) {
	home, path := newPage(t, oneBlock)
	killMidRestore(t, home, path)
	if got := onDisk(t, path); got != oneBlockUno {
		t.Fatalf("page after the kill is %q, want kapi's edit with the agent's write displaced", got)
	}
	s := open(t, home, path)
	if src, _ := s.Source(); !strings.Contains(string(src), "agent wrote this") {
		t.Errorf("after reopening, the source is %q; the agent's write is lost", src)
	}
	if got := onDisk(t, path); !strings.Contains(got, "agent wrote this") {
		t.Errorf("after reopening, the page file is %q; the agent's write is lost", got)
	}
	tick(t, s, heldTicks)
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*.kapi-*")); len(left) > 0 {
		t.Errorf("temp files left after %d refreshes: %v", heldTicks, left)
	}
}

func TestASwapLostToAnAgentLeavesNoRevisionInFlight(t *testing.T) {
	home, path := newPage(t, oneBlock)
	s := open(t, home, path)
	replaceHook = func(stage string) {
		if stage == "swap" {
			agentRename(t, path, agentWrite)
		}
	}
	_, _, err := s.Apply(1, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
	replaceHook = nil
	if !errors.Is(err, ErrStale) {
		t.Fatalf("Apply err = %v, want ErrStale", err)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil || st.Rev != 1 || st.Next != nil {
		t.Errorf("state.json = %s, %v; want revision 1 with nothing in flight", b, err)
	}
}

func TestOpenKeepsADisplacedAgentWriteWhenThePageChangedSince(t *testing.T) {
	home, path := newPage(t, oneBlock)
	killMidRestore(t, home, path)
	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\">newer</p>\n")
	open(t, home, path)
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".plan.html.kapi-kept-*"))
	if len(left) != 1 {
		t.Fatalf("kept files %v, want the displaced agent write set aside", left)
	}
	if b, err := os.ReadFile(left[0]); err != nil || string(b) != agentWrite {
		t.Errorf("temp file now %q, %v; want the agent's displaced write kept", b, err)
	}
	if got := onDisk(t, path); !strings.Contains(got, ">newer<") {
		t.Errorf("page = %q, want the newer edit kept", got)
	}
}

func TestOpenNeverSwapsInALeftoverTempAndKeepsItsBytesAside(t *testing.T) {
	for name, temp := range map[string]string{"empty": "", "partial": `<p data-kapi-id="aaaaaaaa">u`} {
		home, path := newPage(t, oneBlock)
		s := open(t, home, path)
		mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
		dir := filepath.Dir(path)
		if err := os.WriteFile(filepath.Join(dir, ".plan.html.kapi-4265065571"), []byte(temp), 0o600); err != nil {
			t.Fatal(err)
		}
		if src, _ := open(t, home, path).Source(); string(src) != oneBlockUno {
			t.Errorf("%s temp: source now %q, want the page unchanged", name, src)
		}
		if b, err := os.ReadFile(filepath.Join(dir, ".plan.html.kapi-kept-4265065571")); err != nil || string(b) != temp {
			t.Errorf("%s temp: kept copy is %q, %v; want its bytes set aside", name, b, err)
		}
		if src, _ := open(t, home, path).Source(); string(src) != oneBlockUno {
			t.Errorf("%s temp: a second open changed the source to %q", name, src)
		}
	}
}

func TestOnlyTheTempTheIntentNamesIsSwappedBackIn(t *testing.T) {
	home, path := newPage(t, oneBlock)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
	b, err := json.Marshal(state{Rev: 1, Next: &state{Rev: 2, Undo: []uint64{1}, Temp: ".plan.html.kapi-2222222222"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "state.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	stale := "<p>stale</p>\n"
	for name, body := range map[string]string{".plan.html.kapi-1111111111": stale, ".plan.html.kapi-2222222222": agentWrite} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	open(t, home, path)
	if got := onDisk(t, path); !strings.Contains(got, "agent wrote this") {
		t.Errorf("page = %q, want the named temp's agent write swapped back", got)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".plan.html.kapi-kept-1111111111")); err != nil || string(b) != stale {
		t.Errorf("unnamed temp kept as %q, %v; want it set aside, never swapped in", b, err)
	}
}

func TestAKeptTempIsNeverSwappedInByALaterCrash(t *testing.T) {
	home, path := newPage(t, oneBlock)
	s := open(t, home, path)
	writeState := func(st state) {
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, "state.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
	intended := "<p data-kapi-id=\"aaaaaaaa\">tres</p>\n"
	if err := os.WriteFile(s.revPath(3), []byte(intended), 0o600); err != nil {
		t.Fatal(err)
	}
	temp := ".plan.html.kapi-1111111111"
	writeState(state{Rev: 2, Undo: []uint64{1}, Next: &state{Rev: 3, Undo: []uint64{1, 2}, Temp: temp}})
	dir := filepath.Dir(path)
	if err := os.WriteFile(filepath.Join(dir, temp), []byte(intended[:12]), 0o600); err != nil {
		t.Fatal(err)
	}
	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\">agent</p>\n")
	s = open(t, home, path)
	before, rev := s.Source()
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">later</p>`})
	after, next := s.Source()
	writeState(state{Rev: rev, Undo: s.undo[:len(s.undo)-1], Next: &state{Rev: next, Undo: s.undo, Temp: ".plan.html.kapi-2222222222"}})
	if err := os.WriteFile(filepath.Join(dir, ".plan.html.kapi-2222222222"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	if src, _ := open(t, home, path).Source(); !bytes.Equal(src, after) {
		t.Fatalf("after the second crash the source is %q, want %q", src, after)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".plan.html.kapi-kept-1111111111")); err != nil || string(b) != intended[:12] {
		t.Errorf("the first crash's temp is %q, %v; want it kept aside", b, err)
	}
}

func TestOpenRemovesOnlyItsOwnTempFilesThatHoldARevision(t *testing.T) {
	home, path := newPage(t, crashStart)
	open(t, home, path)
	dir := filepath.Dir(path)
	for _, name := range []string{".plan.html.kapi-123", ".other.html.kapi-123", ".plan.html.kapi-notes", "plan.html.kapi-123"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(crashStart), 0o600); err != nil {
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

func TestAnAdoptedRevisionIsNeverReusedAfterTheNextRestart(t *testing.T) {
	home, path := newPage(t, oneBlock)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "edit", ID: "aaaaaaaa", HTML: `<p data-kapi-id="aaaaaaaa">uno</p>`})
	b, err := json.Marshal(state{Rev: 1, Undo: []uint64{}, Next: &state{Rev: 2, Undo: []uint64{1}, Temp: ".plan.html.kapi-1234"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "state.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, adopted := open(t, home, path).Source(); adopted != 2 {
		t.Fatalf("adopted rev %d, want 2", adopted)
	}
	externalWrite(t, path, "<p data-kapi-id=\"aaaaaaaa\">agent</p>\n")
	if _, got := open(t, home, path).Source(); got <= 2 {
		t.Fatalf("after a second restart the agent's edit is rev %d, want above the adopted rev 2", got)
	}
}
