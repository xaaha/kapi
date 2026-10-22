package eventlog

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

var crashTexts = []string{"one", "two", "three", "four"}

var positionWrites = [][]func(*Log, uint64) error{
	nil,
	{func(l *Log, seq uint64) error { return l.Ack("agent", seq) }},
	{func(l *Log, seq uint64) error { return l.Queue("agent", seq) }},
	{func(l *Log, seq uint64) error { return l.Handover("agent", seq, []uint64{seq}) }, func(l *Log, seq uint64) error { return l.Ack("agent", seq) }},
}

func traceWrites(on func(kind, name string)) (restore func()) {
	sync0, rename0 := syncFile, renameFile
	syncFile = func(f *os.File) error { on("sync", f.Name()); return sync0(f) }
	renameFile = func(a, b string) error { on("rename", b); return rename0(a, b) }
	return func() { syncFile, renameFile = sync0, rename0 }
}

func runCrashScenario(dir string, after func(*Log)) error {
	l, err := Open(dir)
	if err != nil {
		return err
	}
	defer l.Close()
	for i, text := range crashTexts {
		if _, err := l.Append(&Event{Author: "human", Kind: "message", Text: text}); err != nil {
			return err
		}
		after(l)
		seq := uint64(i + 1)
		for _, write := range positionWrites[i] {
			if err := write(l, seq); err != nil {
				return err
			}
			after(l)
		}
	}
	return nil
}

func TestCrashChildProcess(t *testing.T) {
	at, err := strconv.Atoi(os.Getenv("KAPI_LOG_CRASH_AT"))
	if err != nil {
		t.Skip("runs only as the child of TestAKilledLogRecoversAtEveryWritePoint")
	}
	n := 0
	defer traceWrites(func(string, string) {
		if n++; n == at {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			select {}
		}
	})()
	if err := runCrashScenario(os.Getenv("KAPI_LOG_CRASH_DIR"), func(*Log) {}); err != nil {
		t.Fatal(err)
	}
}

func TestAKilledLogRecoversAtEveryWritePoint(t *testing.T) {
	n := 0
	restore := traceWrites(func(string, string) { n++ })
	err := runCrashScenario(t.TempDir(), func(*Log) {})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= n+1; at++ {
		t.Run(strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cmd := exec.Command(exe, "-test.run=^TestCrashChildProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), "KAPI_LOG_CRASH_AT="+strconv.Itoa(at), "KAPI_LOG_CRASH_DIR="+dir)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			killed := errors.As(err, &exit) && exit.Sys().(syscall.WaitStatus).Signal() == syscall.SIGKILL
			if killed != (at <= n) || (!killed && err != nil) {
				t.Fatalf("child at write %d of %d: %v\n%s", at, n, err, out)
			}
			l, err := Open(dir)
			if err != nil {
				t.Fatalf("reopen after a kill at write %d: %v", at, err)
			}
			defer l.Close()
			events, _ := l.After(0)
			for i, e := range events {
				if i >= len(crashTexts) || e.Text != crashTexts[i] || e.Seq != uint64(i+1) {
					t.Fatalf("after a kill at write %d, event %d is %+v", at, i, e)
				}
			}
			for name, p := range map[string]uint64{"seen": l.Cursor("agent"), "queued": l.Queued("agent"), "hooked": l.Hooked("agent")} {
				if p > uint64(len(events)) {
					t.Errorf("after a kill at write %d, %s position %d is past the %d events", at, name, p, len(events))
				}
			}
			if e, err := l.Append(&Event{Author: "human", Kind: "message", Text: "after"}); err != nil || e.Seq != uint64(len(events)+1) {
				t.Errorf("append after a kill at write %d = %+v, %v", at, e, err)
			}
		})
	}
}

func TestEveryLogWriteIsSyncedBeforeItIsReported(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, eventsFile)
	var trace []string
	restore := traceWrites(func(kind, name string) {
		if kind == "sync" && name == events {
			b, _ := os.ReadFile(events)
			name += "@" + strconv.Itoa(bytes.Count(b, []byte("\n")))
		}
		trace = append(trace, kind+" "+name)
	})
	defer restore()
	appended := 0
	err := runCrashScenario(dir, func(l *Log) {
		all, _ := l.After(0)
		if len(all) > appended {
			appended = len(all)
			if want := "sync " + events + "@" + strconv.Itoa(appended); trace[len(trace)-1] != want {
				t.Errorf("Append %d returned without %s; trace %v", appended, want, trace)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
