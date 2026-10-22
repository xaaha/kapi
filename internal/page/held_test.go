package page

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func heldFiles(t *testing.T, s *Store) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(s.heldDir(), "*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

func TestEachHeldFileRetiresByItsOwnClockAndNotBefore(t *testing.T) {
	now := time.Now()
	clock = func() time.Time { return now }
	defer func() { clock = time.Now }()
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	now = now.Add(heldFor / 2)
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})
	tick(t, s, 5)
	if got := len(heldFiles(t, s)); got != 2 {
		t.Fatalf("held files before either expired = %d, want 2", got)
	}
	now = now.Add(heldFor/2 + time.Millisecond)
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if got := len(heldFiles(t, s)); got != 1 {
		t.Fatalf("held files after the first expired = %d, want 1", got)
	}
	now = now.Add(heldFor)
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if left := heldFiles(t, s); len(left) != 0 {
		t.Errorf("held files after both expired: %v", left)
	}
	if left := r51left(t, path); len(left) != 0 {
		t.Errorf("files next to the page: %v", left)
	}
}

func TestSettleSwapsBackALateWriteAndRemovesEveryHeldFile(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	time.Sleep(5 * time.Millisecond)
	r51write(t, *fp, late)
	if err := s.Settle(); err != nil {
		t.Fatal(err)
	}
	if got := onDisk(t, path); got != late {
		t.Errorf("page after Settle = %q, want the late write %q", got, late)
	}
	if left := heldFiles(t, s); len(left) != 0 {
		t.Errorf("held files after Settle: %v", left)
	}
}

const swapBackLate = "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"

func TestSwapBackChildProcess(t *testing.T) {
	at, err := strconv.Atoi(os.Getenv("KAPI_SWAPBACK_AT"))
	if err != nil {
		t.Skip("runs only as the child of TestAKilledSwapBackKeepsTheAgentsLateWrite")
	}
	home, path := os.Getenv("KAPI_SWAPBACK_HOME"), os.Getenv("KAPI_SWAPBACK_PAGE")
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	r51write(t, *fp, swapBackLate)
	n := 0
	defer traceWrites(func(writeEvent) {
		if n++; n == at {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			select {}
		}
	})()
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
}

func TestAKilledSwapBackKeepsTheAgentsLateWrite(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	replaceHook = nil
	archived := onDisk(t, path)
	r51write(t, *fp, swapBackLate)
	n := 0
	restore := traceWrites(func(writeEvent) { n++ })
	_, _, err := s.Refresh()
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk(t, path); got != swapBackLate {
		t.Fatalf("the dry run did not swap the late write back: page %q", got)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= n; at++ {
		t.Run(strconv.Itoa(at), func(t *testing.T) {
			t.Parallel()
			home, path := newPage(t, r51start)
			cmd := exec.Command(exe, "-test.run=^TestSwapBackChildProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), "KAPI_SWAPBACK_AT="+strconv.Itoa(at), "KAPI_SWAPBACK_HOME="+home, "KAPI_SWAPBACK_PAGE="+path)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("child at write %d of %d was not killed: %v\n%s", at, n, err, out)
			}
			s := open(t, home, path)
			kept, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-kept-*"))
			found := onDisk(t, path) == swapBackLate
			for _, k := range kept {
				b, _ := os.ReadFile(k)
				found = found || string(b) == swapBackLate
			}
			if !found {
				t.Errorf("kill at write %d: page %q, kept %v; the agent's late write is gone", at, onDisk(t, path), kept)
			}
			if left := heldFiles(t, s); len(left) != 0 {
				t.Errorf("kill at write %d: held files after reopen: %v", at, left)
			}
			for _, f := range r51left(t, path) {
				if !strings.Contains(f, "kapi-kept-") {
					t.Errorf("kill at write %d: temp left next to the page: %s", at, f)
				}
			}
			for _, k := range kept {
				if b, _ := os.ReadFile(k); string(b) == archived {
					t.Errorf("kill at write %d: kapi's own version was set aside as %s", at, k)
				}
			}
		})
	}
}

func TestARetriedCommitFirstSwapsBackALateWrite(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	fp := r51holdAtSwap(t, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	agentRename(t, path, "<p>agent one</p>\n")
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	raced := false
	replaceHook = func(stage string) {
		if stage == "swap" && !raced {
			raced = true
			agentRename(t, path, "<p>agent two</p>\n")
			r51write(t, *fp, late)
		}
	}
	_, _, err := s.Refresh()
	replaceHook = nil
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk(t, path); got != late {
		t.Errorf("after one refresh the page is %q, want the late write %q swapped back before the retried commit", got, late)
	}
}
