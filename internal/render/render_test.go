package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func render(t *testing.T, src string) string {
	t.Helper()
	out, err := Render([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if bytes.Equal(got, want) {
		return
	}
	g, w := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < max(len(g), len(w)); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			t.Errorf("%s line %d differs (go test ./internal/render -update rewrites it)\ngot:  %s\nwant: %s", path, i+1, gl, wl)
			return
		}
	}
}

func TestGoldenCases(t *testing.T) {
	inputs, err := filepath.Glob("testdata/cases/*.html")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no cases: %v", err)
	}
	for _, in := range inputs {
		t.Run(strings.TrimSuffix(filepath.Base(in), ".html"), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got := render(t, string(src))
			golden(t, strings.TrimSuffix(in, ".html")+".golden", []byte(got))
		})
	}
}

func samplePage(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile("testdata/all-presets.src.html")
	if err != nil {
		t.Fatal(err)
	}
	body := render(t, string(src))
	var b bytes.Buffer
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<title>kapi presets</title>\n<style>\n")
	b.Write(CSS())
	b.WriteString("</style>\n</head>\n<body>\n<main id=\"kapi-page\">\n")
	b.WriteString(body)
	b.WriteString("\n</main>\n</body>\n</html>\n")
	return b.Bytes()
}

func TestAllPresetsSample(t *testing.T) {
	page := samplePage(t)
	golden(t, "testdata/all-presets.html", page)
	for _, p := range Presets() {
		if !bytes.Contains(page, []byte(`class="`+p.Name)) {
			t.Errorf("sample page has no rendered %s", p.Name)
		}
	}
}
