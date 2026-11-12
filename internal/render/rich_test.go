package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestRichRenderKeepsHTMLAndCSSButDropsExecution(t *testing.T) {
	out := render(t, `<kapi-render title="Prototype">
<style>.cards{display:grid;background:var(--kapi-surface)} @import "https://evil.test/x.css";</style>
<div class="cards" onclick="bad()"><button>Open</button><svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"></circle></svg><script>bad()</script></div>
</kapi-render>`)
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	frame := findTag(doc, "iframe")
	if frame == nil {
		t.Fatalf("rendered no iframe: %s", out)
	}
	srcdoc := attrVal(frame, "srcdoc")
	for _, want := range []string{`.cards{display:grid`, `var(--kapi-surface)`, `<button>Open</button>`, `<svg`, `<circle`} {
		if !strings.Contains(srcdoc, want) {
			t.Errorf("srcdoc does not keep %q:\n%s", want, srcdoc)
		}
	}
	for _, banned := range []string{"@import", "onclick", "<script", "bad()"} {
		if strings.Contains(srcdoc, banned) {
			t.Errorf("srcdoc keeps unsafe %q:\n%s", banned, srcdoc)
		}
	}
}

func TestPresetWarningsKeepSafeContentVisible(t *testing.T) {
	out := render(t, `<kapi-callout kind="alarm"><p>Keep this explanation.</p></kapi-callout>`)
	if !strings.Contains(out, "kapi-error") || !strings.Contains(out, "Keep this explanation.") {
		t.Errorf("warning replaced the content: %s", out)
	}
}

func TestCalloutAcceptsCommonAgentAliases(t *testing.T) {
	for alias, class := range map[string]string{"warn": "warning", "info": "note", "error": "danger", "done": "success"} {
		out := render(t, `<kapi-callout kind="`+alias+`">x</kapi-callout>`)
		if !strings.Contains(out, "kapi-callout-"+class) || strings.Contains(out, "kapi-error") {
			t.Errorf("kind %q did not become %q: %s", alias, class, out)
		}
	}
}
