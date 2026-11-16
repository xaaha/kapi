package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var arrowMarker = regexp.MustCompile(`kapi-arrow-[0-9a-f]{8}`)

func drawing(s string) string {
	return arrowMarker.ReplaceAllString(s, "kapi-arrow")
}

func TestEveryMistakeRendersStyledWithoutAnErrorBox(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "mistakes", "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no mistake files: %v", err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".html"), func(t *testing.T) {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			mistake, want, repaired := splitMistake(t, string(b))
			got := drawing(render(t, mistake))
			if strings.Contains(got, "kapi-error") {
				t.Fatalf("the reader sees an error box:\n%s", got)
			}
			if styled := drawing(render(t, want)); got != styled {
				t.Errorf("repaired output differs from the styled form\ngot  %s\nwant %s", got, styled)
			}
			notes, err := Repairs([]byte(mistake))
			if err != nil {
				t.Fatal(err)
			}
			if repaired != (len(notes) > 0) {
				t.Errorf("notes = %q, want notes: %v", notes, repaired)
			}
			if wantNotes, _ := Repairs([]byte(want)); len(wantNotes) > 0 {
				t.Errorf("the styled form itself needs repairs: %q", wantNotes)
			}
		})
	}
}

func splitMistake(t *testing.T, s string) (mistake, want string, repaired bool) {
	t.Helper()
	for _, mark := range []string{"<!-- repaired to -->", "<!-- renders as -->"} {
		if before, after, ok := strings.Cut(s, "\n"+mark+"\n"); ok {
			return before, strings.TrimSuffix(after, "\n"), mark == "<!-- repaired to -->"
		}
	}
	t.Fatal("the file needs a <!-- repaired to --> or <!-- renders as --> line between the mistake and its styled form")
	return "", "", false
}
