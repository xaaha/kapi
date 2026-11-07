//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

const missingFileText = "no such file"

const exeSuffix = ""

func (e *e2e) fakeOpeners(dir string) {
	e.t.Helper()
	bin := filepath.Join(dir, "openers")
	if err := os.Mkdir(bin, 0o700); err != nil {
		e.t.Fatal(err)
	}
	e.opened = filepath.Join(dir, "opened")
	script := "#!/bin/sh\necho \"$1\" >> '" + e.opened + "'\n"
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil { //nolint:gosec // a test opener must be executable
			e.t.Fatal(err)
		}
	}
	e.env = append(e.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (e *e2e) killDaemon() {
	if out, err := exec.Command("pkill", "-f", e.bin+" daemon").CombinedOutput(); err != nil && len(out) > 0 { //nolint:gosec // matches only the kapi binary this test just built
		e.t.Logf("pkill: %v %s", err, out)
	}
	waitFor(e.t, "the daemon to exit", func() bool {
		return exec.Command("pgrep", "-f", e.bin+" daemon").Run() != nil //nolint:gosec // matches only the kapi binary this test just built
	})
}
