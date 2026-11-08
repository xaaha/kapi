package page

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreviewIsTheFirstHeadingAndParagraph(t *testing.T) {
	for src, want := range map[string][2]string{
		`<h1 data-kapi-id="a">Rollout <em>plan</em></h1><p>Ship it.</p><h1>Second</h1>`: {"Rollout plan", "Ship it."},
		"<p>intro\n  text</p>\n<h1>\n  Spread\n  out\n</h1>":                            {"Spread out", "intro text"},
		`<h2>Not a title</h2><p>body</p>`:                                               {"", "body"},
		`<h1>  </h1><p> </p><h1>Real</h1><p>Tom &amp; Jerry</p>`:                        {"Real", "Tom & Jerry"},
		`<kapi-callout><p>Inside a callout</p></kapi-callout>`:                          {"", "Inside a callout"},
	} {
		title, summary := Preview([]byte(src))
		if title != want[0] || summary != want[1] {
			t.Errorf("Preview(%q) = %q, %q, want %q, %q", src, title, summary, want[0], want[1])
		}
	}
}

func TestPreviewCutsALongParagraphAtAWord(t *testing.T) {
	_, summary := Preview([]byte("<p>" + strings.Repeat("word ", 100) + "</p>"))
	if !strings.HasSuffix(summary, "word…") || utf8.RuneCountInString(summary) > summaryRunes+1 {
		t.Errorf("summary = %q", summary)
	}
}
