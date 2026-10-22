package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func (e *e2e) daemonPIDs() []int {
	e.t.Helper()
	out, _ := exec.Command("pgrep", "-f", e.bin+" daemon").Output() //nolint:gosec // matches only the kapi binary this test just built
	var pids []int
	for f := range strings.FieldsSeq(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			e.t.Fatal(err)
		}
		pids = append(pids, pid)
	}
	return pids
}

func parentOf(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // a pid this test found
	if err != nil {
		t.Fatalf("ps for %d: %v", pid, err)
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return ppid
}

func killTree(t *testing.T, pid int) {
	t.Helper()
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output() //nolint:gosec // a pid this test started
	for f := range strings.FieldsSeq(string(out)) {
		child, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		killTree(t, child)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestAnAutostartedDaemonOutlivesTheClientsProcessTree(t *testing.T) {
	e := newE2E(t)
	cmd := exec.Command(e.bin, "watch", e.page) //nolint:gosec // runs the kapi binary this test just built
	cmd.Env = e.env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the socket and one daemon", func() bool {
		_, err := os.Stat(filepath.Join(e.home, "kapi.sock"))
		return err == nil && len(e.daemonPIDs()) == 1
	})
	daemon := e.daemonPIDs()[0]
	if ppid := parentOf(t, daemon); ppid != 1 {
		t.Errorf("daemon %d has parent %d, want 1 (the watch is %d)", daemon, ppid, cmd.Process.Pid)
	}

	killTree(t, cmd.Process.Pid)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()
	time.Sleep(200 * time.Millisecond)
	if !alive(daemon) {
		t.Fatal("killing the watch's process tree and group killed the daemon")
	}
	e.kapi("send", e.page, "still here")
	if got := e.daemonPIDs(); len(got) != 1 || got[0] != daemon {
		t.Errorf("daemon processes %v after the kill, want the same daemon %d", got, daemon)
	}
}

func TestClientsAutostartingAtOnceGetOneDaemon(t *testing.T) {
	e := newE2E(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	outs := make([][]byte, len(errs))
	for i := range errs {
		wg.Go(func() {
			cmd := exec.Command(e.bin, "send", e.page, "hello "+strconv.Itoa(i)) //nolint:gosec // runs the kapi binary this test just built
			cmd.Env = e.env
			outs[i], errs[i] = cmd.CombinedOutput()
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("client %d: %v\n%s", i, err, outs[i])
		}
	}
	waitFor(t, "one daemon", func() bool { return len(e.daemonPIDs()) == 1 })
	if got := strings.Count(e.kapi("pending", e.page), "human message: hello"); got != len(errs) {
		t.Errorf("%d messages pending, want %d", got, len(errs))
	}
}

func TestAFailedSpawnIsReportedAtOnce(t *testing.T) {
	e := newE2E(t)
	if err := os.Mkdir(filepath.Join(e.home, "daemon.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.bin, "pending", e.page) //nolint:gosec // runs the kapi binary this test just built
	cmd.Env = e.env
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "start daemon") || !strings.Contains(string(out), "daemon.log") {
		t.Errorf("kapi pending: %v\n%s; want a start daemon error naming daemon.log", err, out)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the failed start took %v, want an error before the socket wait runs out", took)
	}
	if pids := e.daemonPIDs(); len(pids) != 0 {
		t.Errorf("daemon processes %v, want none", pids)
	}
}
