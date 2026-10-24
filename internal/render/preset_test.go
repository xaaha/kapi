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
		`<li class="kapi-task" data-kapi-preset="task" data-kapi-id="aaaaaaaa"><input type="checkbox" checked="" aria-label="Copy &amp; check"><span class="kapi-task-body" data-kapi-text="aaaaaaaa">Copy &amp; check</span></li>` +
		`<li class="kapi-task" data-kapi-preset="task" data-kapi-id="bbbbbbbb"><input type="checkbox" aria-label="Flip"><span class="kapi-task-body" data-kapi-text="bbbbbbbb">Flip</span></li></ul>`
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

func TestBadgeStateAddsAStatusIconAndDefaultTone(t *testing.T) {
	for state, tone := range map[string]string{"done": "green", "failed": "red", "blocked": "red", "progress": "orange", "waiting": "grey", "info": "blue"} {
		out := render(t, `<p>x <kapi-badge state="`+state+`">label</kapi-badge></p>`)
		if !strings.Contains(out, `<span class="kapi-badge kapi-tone-`+tone+`" data-kapi-preset="badge"><svg class="kapi-icon" viewBox="0 0 256 256" aria-hidden="true">`) || !strings.HasSuffix(out, `</svg>label</span></p>`) {
			t.Errorf("state %s: want a %s badge led by an icon:\n%s", state, tone, out)
		}
		out = render(t, `<p>x <kapi-badge state="`+state+`"></kapi-badge></p>`)
		if !strings.Contains(out, `<span class="kapi-badge-icon kapi-tone-`+tone+`" data-kapi-preset="badge" role="img" aria-label="`+statusIcons[state].label+`"><svg class="kapi-icon"`) {
			t.Errorf("state %s with no body: want a bare labelled icon:\n%s", state, out)
		}
	}
	if out := render(t, `<p><kapi-badge state="done" tone="blue">shipped</kapi-badge></p>`); !strings.Contains(out, "kapi-tone-blue") {
		t.Errorf("an explicit tone should win over the state's tone: %s", out)
	}
	out := render(t, `<p><kapi-badge state="maybe">x</kapi-badge></p>`)
	if !strings.Contains(out, "kapi-error") || !strings.Contains(out, "done, failed, blocked, progress, waiting or info") {
		t.Errorf("an unknown state should be an error naming the states: %s", out)
	}
}

func TestStepStateShowsItsIconInPlaceOfTheNumber(t *testing.T) {
	out := render(t, `<kapi-plan><kapi-step state="done">a</kapi-step><kapi-step>b</kapi-step></kapi-plan>`)
	if !strings.Contains(out, `<div class="kapi-plan-item kapi-step kapi-tone-green" data-kapi-preset="step"><span class="kapi-plan-mark kapi-plan-state" role="img" aria-label="Done"><svg class="kapi-icon"`) {
		t.Errorf("a done step should show a green check mark:\n%s", out)
	}
	if !strings.Contains(out, `<div class="kapi-plan-item kapi-step kapi-tone-purple" data-kapi-preset="step"><span class="kapi-plan-mark"></span>`) {
		t.Errorf("a step without state keeps its number:\n%s", out)
	}
	if out := render(t, `<kapi-plan><kapi-step state="soon">a</kapi-step></kapi-plan>`); !strings.Contains(out, "kapi-error") {
		t.Errorf("an unknown step state should be an error: %s", out)
	}
}
