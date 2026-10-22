package render

import (
	"strings"
	"testing"
)

func TestCalloutKindsMapToTones(t *testing.T) {
	for kind, want := range map[string]string{"": "info", "note": "info", "warning": "warning", "key": "accent", "danger": "danger", "success": "success"} {
		out := render(t, `<kapi-callout kind="`+kind+`">x</kapi-callout>`)
		if !strings.Contains(out, " kapi-tone-"+want+`"`) || strings.Contains(out, "kapi-error") {
			t.Errorf("kind %q: want tone %s:\n%s", kind, want, out)
		}
	}
	out := render(t, `<kapi-callout kind="alarm">x</kapi-callout>`)
	if !strings.Contains(out, "kapi-error") || !strings.Contains(out, "note, warning, key, danger or success") {
		t.Errorf("unknown kind should name every kind in an error box:\n%s", out)
	}
}

func TestBadgeTakesOneOfTheSixTones(t *testing.T) {
	for _, tone := range tones {
		out := render(t, `<p>x <kapi-badge tone="`+tone+`">merged</kapi-badge></p>`)
		want := `<p>x <span class="kapi-badge kapi-tone-` + tone + `" data-kapi-preset="badge">merged</span></p>`
		if out != want {
			t.Errorf("tone %s:\n got %s\nwant %s", tone, out, want)
		}
	}
	if out := render(t, `<kapi-badge>draft</kapi-badge>`); !strings.Contains(out, "kapi-tone-neutral") {
		t.Errorf("a badge with no tone should be neutral: %s", out)
	}
	out := render(t, `<p>x <kapi-badge tone="purple">y</kapi-badge></p>`)
	if !strings.HasPrefix(out, `<p>x <span class="kapi-error"`) || !strings.Contains(out, "neutral, info, success, warning, danger or accent") {
		t.Errorf("an unknown tone should be an inline error box naming the tones: %s", out)
	}
}

func TestBadgeStaysInlineAndPushesBlocksOut(t *testing.T) {
	out := render(t, `<table><tr><td>a <kapi-badge tone="info">open</kapi-badge></td></tr></table>`)
	if !strings.Contains(out, `<td>a <span class="kapi-badge kapi-tone-info" data-kapi-preset="badge">open</span></td>`) {
		t.Errorf("badge should stay in its table cell: %s", out)
	}
	out = render(t, `<p>a <kapi-badge tone="info">b <kapi-callout>c</kapi-callout> d</kapi-badge> e</p>`)
	assertParseStable(t, "callout in badge", out)
	if strings.Contains(out, `badge">b <aside`) {
		t.Errorf("a callout inside a badge should move out of it: %s", out)
	}
}
