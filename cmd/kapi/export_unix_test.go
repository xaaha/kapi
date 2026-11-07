//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestD23AnExportFollowsTheUmask(t *testing.T) {
	page := exportPage(t)
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })
	for _, force := range []bool{false, true} {
		outDir := t.TempDir()
		args := []string{"export", "--md", "--out", outDir, page}
		if force {
			args = []string{"export", "--md", "--force", "--out", outDir, page}
		}
		if _, err := run(t, &fakeAPI{}, args...); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(outDir, "plan.md"))
		if err != nil || info.Mode().Perm() != 0o640 {
			t.Errorf("force=%v: mode %v (%v), want -rw-r----- under umask 027", force, info.Mode().Perm(), err)
		}
	}
}
