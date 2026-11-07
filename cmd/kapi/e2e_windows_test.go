package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const missingFileText = "cannot find the file"

const exeSuffix = ".exe"

func (e *e2e) fakeOpeners(dir string) {
	e.t.Helper()
	bin := filepath.Join(dir, "openers")
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "rundll32.exe"), "./testdata/fakeopener").CombinedOutput(); err != nil {
		e.t.Fatalf("go build fakeopener: %v\n%s", err, out)
	}
	e.opened = filepath.Join(dir, "opened")
	e.env = append(e.env, "KAPI_FAKE_OPENED="+e.opened, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (e *e2e) daemonQuery(then string) string {
	e.t.Helper()
	script := "Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq '" + e.bin + "' }" + then
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput() //nolint:gosec // matches only the kapi binary this test just built
	if err != nil {
		e.t.Logf("powershell: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func (e *e2e) killDaemon() {
	e.daemonQuery(" | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }")
	waitFor(e.t, "the daemon to exit", func() bool { return e.daemonQuery(" | Measure-Object | ForEach-Object Count") == "0" })
}
