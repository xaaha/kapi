package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestZZProbeDetachedStderr(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "probe.exe")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/stderrprobe").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for name, flags := range map[string]uint32{"none": 0, "detached": windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, "nowindow": windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP} {
		logPath := filepath.Join(dir, name+".log")
		note := filepath.Join(dir, name+".note")
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, note)
		cmd.Stdout, cmd.Stderr = f, f
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		start := time.Now()
		err = cmd.Start()
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		b, _ := os.ReadFile(logPath)
		n, _ := os.ReadFile(note)
		t.Errorf("PROBE %s: took %v log=%q note=%q", name, time.Since(start), b, n)
	}
}
