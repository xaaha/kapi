package main

import (
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/render"
)

func TestPresetListsEveryPreset(t *testing.T) {
	out, err := run(t, &fakeAPI{}, "preset")
	if err != nil {
		t.Fatal(err)
	}
	presets := render.Presets()
	if !strings.HasPrefix(out, "presets[8]:\n") || len(presets) != 8 {
		t.Errorf("output does not start with the preset count:\n%s", out)
	}
	for _, p := range presets {
		if !strings.Contains(out, p.Name) || !strings.Contains(out, p.Summary) {
			t.Errorf("output misses %s and its summary:\n%s", p.Name, out)
		}
	}
	for _, want := range []string{"rules:", "data-kapi-id", "kapi preset <name>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
}

func TestPresetShowsExample(t *testing.T) {
	for _, name := range []string{"diagram", "kapi-diagram"} {
		out, err := run(t, &fakeAPI{}, "preset", name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, "kapi-diagram: ") || !strings.Contains(out, `<kapi-diagram type="sequence">`) || !strings.Contains(out, "type: flow") {
			t.Errorf("preset %s output:\n%s", name, out)
		}
	}
}

func TestPresetUnknownNameListsChoices(t *testing.T) {
	_, err := run(t, &fakeAPI{}, "preset", "chart")
	if err == nil || !strings.Contains(err.Error(), `no preset "chart"`) || !strings.Contains(err.Error(), "callout, code, diff") {
		t.Errorf("err = %v", err)
	}
}
