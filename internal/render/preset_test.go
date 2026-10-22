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
