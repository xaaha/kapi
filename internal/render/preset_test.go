package render

import (
	"strings"
	"testing"
)

func TestCalloutKindsMapToTones(t *testing.T) {
	for kind, want := range map[string]string{"": "blue", "note": "blue", "warning": "yellow", "key": "purple", "danger": "red", "success": "green"} {
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

func TestBadgeTakesOneOfTheSevenTones(t *testing.T) {
	for _, tone := range tones {
		out := render(t, `<p>x <kapi-badge tone="`+tone+`">merged</kapi-badge></p>`)
		want := `<p>x <span class="kapi-badge kapi-tone-` + tone + `" data-kapi-preset="badge">merged</span></p>`
		if out != want {
			t.Errorf("tone %s:\n got %s\nwant %s", tone, out, want)
		}
	}
	if out := render(t, `<kapi-badge>draft</kapi-badge>`); !strings.Contains(out, "kapi-tone-grey") {
		t.Errorf("a badge with no tone should be grey: %s", out)
	}
	out := render(t, `<p>x <kapi-badge tone="teal">y</kapi-badge></p>`)
	if !strings.HasPrefix(out, `<p>x <span class="kapi-error"`) || !strings.Contains(out, "red, orange, yellow, green, blue, purple or grey") {
		t.Errorf("an unknown tone should be an inline error box naming the tones: %s", out)
	}
}

func TestBadgeStaysInlineAndPushesBlocksOut(t *testing.T) {
	out := render(t, `<table><tr><td>a <kapi-badge tone="blue">open</kapi-badge></td></tr></table>`)
	if !strings.Contains(out, `<td>a <span class="kapi-badge kapi-tone-blue" data-kapi-preset="badge">open</span></td>`) {
		t.Errorf("badge should stay in its table cell: %s", out)
	}
	out = render(t, `<p>a <kapi-badge tone="blue">b <kapi-callout>c</kapi-callout> d</kapi-badge> e</p>`)
	assertParseStable(t, "callout in badge", out)
	if strings.Contains(out, `badge">b <aside`) {
		t.Errorf("a callout inside a badge should move out of it: %s", out)
	}
}

func TestKVRendersADefinitionList(t *testing.T) {
	out := render(t, `<kapi-kv><kapi-item key="Owner">Pratik &amp; co</kapi-item><kapi-item key="Due">Friday</kapi-item></kapi-kv>`)
	want := `<dl class="kapi-kv" data-kapi-preset="kv">` +
		`<div class="kapi-kv-item" data-kapi-preset="item"><dt>Owner</dt><dd>Pratik &amp; co</dd></div>` +
		`<div class="kapi-kv-item" data-kapi-preset="item"><dt>Due</dt><dd>Friday</dd></div></dl>`
	if out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	for _, src := range []string{
		`<kapi-kv>text <kapi-item key="a">b</kapi-item></kapi-kv>`,
		`<kapi-kv><p>x</p><kapi-item key="a">b</kapi-item></kapi-kv>`,
		`<kapi-kv><kapi-item>b</kapi-item></kapi-kv>`,
		`<kapi-kv></kapi-kv>`,
		`<kapi-item key="a">b</kapi-item>`,
	} {
		if out := render(t, src); !strings.Contains(out, "kapi-error") {
			t.Errorf("Render(%q) = %s, want an error box", src, out)
		}
	}
}

func TestDetailsIsClosedUnlessOpen(t *testing.T) {
	out := render(t, `<kapi-details summary="Why &lt;two&gt; steps?"><p>Because.</p></kapi-details>`)
	want := `<details class="kapi-details" data-kapi-preset="details"><summary>Why &lt;two&gt; steps?</summary><div class="kapi-details-body"><p>Because.</p></div></details>`
	if out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	if out := render(t, `<kapi-details summary="s" open>x</kapi-details>`); !strings.HasPrefix(out, `<details class="kapi-details" data-kapi-preset="details" open="">`) {
		t.Errorf("open should carry over: %s", out)
	}
	if out := render(t, `<kapi-details>x</kapi-details>`); !strings.Contains(out, "needs a summary attribute") {
		t.Errorf("a missing summary should be an error box: %s", out)
	}
	if got := canonical(t, `<kapi-details summary="s" open onclick="x()">x</kapi-details>`); got != `<kapi-details summary="s" open="">x</kapi-details>` {
		t.Errorf("Canonical = %s", got)
	}
}

func TestChecklistRendersTickableTasks(t *testing.T) {
	out := render(t, `<kapi-checklist><kapi-task done data-kapi-id="aaaaaaaa">Copy &amp; check</kapi-task><kapi-task data-kapi-id="bbbbbbbb">Flip</kapi-task></kapi-checklist>`)
	want := `<ul class="kapi-checklist" data-kapi-preset="checklist">` +
		`<li class="kapi-task" data-kapi-preset="task" data-kapi-id="aaaaaaaa"><label><input type="checkbox" checked=""><span class="kapi-task-body">Copy &amp; check</span></label></li>` +
		`<li class="kapi-task" data-kapi-preset="task" data-kapi-id="bbbbbbbb"><label><input type="checkbox"><span class="kapi-task-body">Flip</span></label></li></ul>`
	if out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	for _, src := range []string{
		`<kapi-checklist>loose <kapi-task>a</kapi-task></kapi-checklist>`,
		`<kapi-checklist></kapi-checklist>`,
		`<kapi-task>a</kapi-task>`,
	} {
		if out := render(t, src); !strings.Contains(out, "kapi-error") {
			t.Errorf("Render(%q) = %s, want an error box", src, out)
		}
	}
	if got := canonical(t, `<kapi-checklist><kapi-task done onclick="x">a</kapi-task></kapi-checklist>`); got != `<kapi-checklist><kapi-task done="">a</kapi-task></kapi-checklist>` {
		t.Errorf("Canonical = %s", got)
	}
}
