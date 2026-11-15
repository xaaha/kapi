package osfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSettledReadsWaitForAFileBeingReplaced(t *testing.T) {
	for name, read := range map[string]func(string) error{
		"ReadSettled": func(p string) error { _, err := ReadSettled(p); return err },
		"StatSettled": func(p string) error { _, err := StatSettled(p); return err },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.html")
			back := make(chan error, 1)
			go func() {
				time.Sleep(30 * time.Millisecond)
				back <- os.WriteFile(path, []byte("agent"), 0o600)
			}()
			if err := read(path); err != nil {
				t.Errorf("a file that reappears 30ms later: %v", err)
			}
			if err := <-back; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSettledReadsReportAGoneFileWithinTheBusyWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.html")
	start := time.Now()
	_, err := ReadSettled(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
	if took := time.Since(start); took > busyWait+time.Second {
		t.Errorf("a missing file took %v to report", took)
	}
}
