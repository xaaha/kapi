package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var radiusDecl = regexp.MustCompile(`border-radius:\s*([^;}]+)`)

var radiusToken = regexp.MustCompile(`^var\(--kapi-(radius(-xs|-sm|-lg)?|frame-radius)\)$`)

func TestEveryCornerRadiusUsesTheSharedScale(t *testing.T) {
	var files []string
	for _, pattern := range []string{"static/*.css", "notebook/files/*.css", "../render/style.css"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range radiusDecl.FindAllStringSubmatch(string(b), -1) {
			for _, v := range strings.Fields(m[1]) {
				if v != "0" && v != "50%" && !radiusToken.MatchString(v) {
					t.Errorf("%s: border-radius %q; use --kapi-radius-xs, -sm, --kapi-radius, -lg, 0, or 50%% for a dot", f, m[1])
				}
			}
		}
	}
}
