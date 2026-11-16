package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestAgentCommandsRestoreAMissingPage(t *testing.T) {
	e := newE2E(t)
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.kapi("open", "--print", e.page)
	want, err := os.ReadFile(e.page)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"say", e.page, "still here?"}, {"open", "--print", e.page}} {
		if err := os.Remove(e.page); err != nil {
			t.Fatal(err)
		}
		if ls := e.kapi("ls"); !strings.Contains(ls, "missing") {
			t.Fatalf("kapi ls with the file gone:\n%s", ls)
		}
		if out := e.kapi(args...); !strings.Contains(out, "was missing; restored version 1 from") {
			t.Errorf("kapi %s printed %q, want it to name the restored version", args[0], out)
		}
		if got, err := os.ReadFile(e.page); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("after kapi %s the file is %q, %v; want %q", args[0], got, err, want)
		}
	}
	e.killDaemon()
	if err := os.Remove(e.page); err != nil {
		t.Fatal(err)
	}
	if out := e.kapi("open", "--print", e.page); !strings.Contains(out, "restored version 1") {
		t.Errorf("kapi open after a restart printed %q", out)
	}
	if ls := e.kapi("ls"); strings.Contains(ls, "missing") {
		t.Errorf("kapi ls after the restore still says missing:\n%s", ls)
	}
}
