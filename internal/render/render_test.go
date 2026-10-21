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
