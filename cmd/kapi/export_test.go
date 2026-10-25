package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestD23ExportWritesMarkdownAndNeverReplacesAFileUnasked(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "plan.html")
	if err := os.WriteFile(page, []byte(`<h1 data-kapi-id="aaaaaaaa">Plan</h1><p data-kapi-id="bbbbbbbb">Ship <em>it</em>.</p>`), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	got, err := run(t, &fakeAPI{}, "export", "--md", "--out", outDir, page)
	want := filepath.Join(outDir, "plan.md")
	if err != nil || got != want+"\n" {
		t.Fatalf("export: %q %v, want the path %s", got, err, want)
	}
	if b, _ := os.ReadFile(want); string(b) != "# Plan\n\nShip *it*.\n" {
		t.Errorf("plan.md = %q", b)
	}
	if err := os.WriteFile(want, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, &fakeAPI{}, "export", "--md", "--out", outDir, page); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("export over an existing file: %v, want a refusal naming --force", err)
	}
	if b, _ := os.ReadFile(want); string(b) != "mine" {
		t.Errorf("the refused export changed the file: %q", b)
	}
	if _, err := run(t, &fakeAPI{}, "export", "--md", "--force", "--out", outDir, page); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(want); !strings.HasPrefix(string(b), "# Plan") {
		t.Errorf("--force did not replace the file: %q", b)
	}
	if got, err := run(t, &fakeAPI{}, "export", "--md", "--out", "-", page); err != nil || got != "# Plan\n\nShip *it*.\n" {
		t.Errorf("--out -: %q %v", got, err)
	}
	if _, err := run(t, &fakeAPI{}, "export", page); err == nil || !strings.Contains(err.Error(), "--md") {
		t.Errorf("no format: %v", err)
	}
}

func TestD23ExportOfAPageWithImagesWritesAZip(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "plan.html")
	name := "0123456789abcdef0123456789abcdef.png"
	if err := os.MkdirAll(filepath.Join(dir, "plan.files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.files", name), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte(`<figure data-kapi-id="aaaaaaaa"><img src="./plan.files/`+name+`" alt=""></figure>`), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if _, err := run(t, &fakeAPI{}, "export", "--md", "--out", outDir, page); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(filepath.Join(outDir, "plan.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 2 || z.File[0].Name != "plan.md" || z.File[1].Name != "images/"+name {
		t.Errorf("zip holds %v", z.File)
	}
}
