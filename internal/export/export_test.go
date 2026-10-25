package export

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var onePixel, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

const (
	shown   = "0123456789abcdef0123456789abcdef.png"
	missing = "ffffffffffffffffffffffffffffffff.png"
)

func filesDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "plan.files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{shown, "secret.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), onePixel, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func unzip(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

func TestD23MarkdownWithoutImagesIsAPlainFile(t *testing.T) {
	src := `<h1 data-kapi-id="aaaaaaaa">Plan</h1><p data-kapi-id="bbbbbbbb">See <img src="https://example.com/x.png" alt="x"> and <img src="./plan.files/` + missing + `" alt="gone"> and <img src="./plan.files/secret.png" alt="s"></p>`
	f, err := Markdown("plan.html", []byte(src), filesDir(t))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Plan\n\nSee ![x](https://example.com/x.png) and ![gone](./plan.files/" + missing + ") and ![s](./plan.files/secret.png)\n"
	if f.Name != "plan.md" || f.Type != "text/markdown; charset=utf-8" || string(f.Data) != want {
		t.Errorf("got %s %s %q, want plan.md with %q", f.Name, f.Type, f.Data, want)
	}
}

func TestD23MarkdownWithPageImagesIsAZipWithAnImagesFolder(t *testing.T) {
	src := `<p data-kapi-id="aaaaaaaa">Chart:</p><figure data-kapi-id="bbbbbbbb"><img src="./plan.files/` + shown + `" alt=""></figure><p data-kapi-id="cccccccc">Again <img src="./plan.files/` + shown + `" alt="again"> and <img src="./plan.files/` + missing + `" alt="gone"></p>`
	f, err := Markdown("my plan.html", []byte(src), filesDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "my plan.zip" || f.Type != "application/zip" {
		t.Fatalf("got %s %s, want my plan.zip", f.Name, f.Type)
	}
	files := unzip(t, f.Data)
	if len(files) != 2 || !bytes.Equal(files["images/"+shown], onePixel) {
		t.Fatalf("zip holds %d files, want my plan.md and images/%s once", len(files), shown)
	}
	md := string(files["my plan.md"])
	want := "Chart:\n\n![](images/" + shown + ")\n\nAgain ![again](images/" + shown + ") and ![gone](./plan.files/" + missing + ")\n"
	if md != want {
		t.Errorf("markdown = %q, want %q", md, want)
	}
	if strings.Contains(md, "secret") {
		t.Error("an unreferenced file went into the zip")
	}
}
