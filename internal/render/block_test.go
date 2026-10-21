package render

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func sources(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob("testdata/cases/*.html")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, "testdata/all-presets.src.html")
	out := map[string][]byte{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = b
	}
	for _, p := range Presets() {
		out["example "+p.Name] = []byte(p.Example)
	}
	return out
}

func kapiIDs(t *testing.T, src []byte) []string {
	t.Helper()
	root, err := parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if id, ok := attr(n, "data-kapi-id"); ok {
				ids = append(ids, id)
			}
		}
		if n.Type == html.ElementNode && (dropped[n.Data] || rawPresets[n.Data]) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		walk(n)
	}
	return ids
}

func TestBlockEqualsFragmentOfRender(t *testing.T) {
	checked := 0
	for name, src := range sources(t) {
		full := render(t, string(src))
		for _, id := range kapiIDs(t, src) {
			block, err := Block(src, id)
			if err != nil {
				t.Errorf("%s: Block(%q): %v", name, id, err)
				continue
			}
			if !bytes.Contains(block, []byte(`data-kapi-id="`+id+`"`)) {
				t.Errorf("%s: Block(%q) lost its data-kapi-id: %s", name, id, block)
			}
			if !strings.Contains(full, string(block)) {
				t.Errorf("%s: Block(%q) is not a fragment of Render:\n%s", name, id, block)
			}
			checked++
		}
	}
	if checked < 100 {
		t.Fatalf("only %d blocks checked; the fixtures lost their ids", checked)
	}
}

func TestBlockUnknownID(t *testing.T) {
	src := []byte(`<p data-kapi-id="aaaaaaaa">x</p><kapi-code data-kapi-id="bbbbbbbb"><p data-kapi-id="cccccccc">inside code</p></kapi-code>`)
	for _, id := range []string{"zzzzzzzz", "cccccccc", ""} {
		if _, err := Block(src, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Block(%q) error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestBlockHidesWhatRenderHides(t *testing.T) {
	cases := map[string]string{
		"inside an unknown preset":   `<kapi-chart><p data-kapi-id="aaaaaaaa">x</p></kapi-chart>`,
		"inside an errored callout":  `<kapi-callout kind="bad"><p data-kapi-id="aaaaaaaa">x</p></kapi-callout>`,
		"inside a misplaced step":    `<kapi-step><p data-kapi-id="aaaaaaaa">x</p></kapi-step>`,
		"inside a form with no name": `<kapi-form><kapi-text name="t" data-kapi-id="aaaaaaaa">x</kapi-text></kapi-form>`,
		"inside a table with prose":  `<kapi-table><p data-kapi-id="aaaaaaaa">x</p></kapi-table>`,
		"inside a dropped element":   `<template><p data-kapi-id="aaaaaaaa">x</p></template>`,
	}
	for name, src := range cases {
		if strings.Contains(render(t, src), "aaaaaaaa") {
			t.Fatalf("%s: Render shows the element, the case is wrong", name)
		}
		if got, err := Block([]byte(src), "aaaaaaaa"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: Block = %q, %v; want ErrNotFound because Render does not show it", name, got, err)
		}
	}
}

func TestBlockNestedPresetKeepsContext(t *testing.T) {
	src := []byte(`<kapi-form name="f"><kapi-choice name="pick" multiple><kapi-opt value="a" data-kapi-id="optaaaaa">A</kapi-opt></kapi-choice></kapi-form>`)
	got, err := Block(src, "optaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`<input type="checkbox" name="pick" value="a">`)) {
		t.Errorf("Block of a nested opt = %s, want the checkbox its choice implies", got)
	}
}

func TestProseRendersAsItsSource(t *testing.T) {
	src := `<h1 data-kapi-id="aaaaaaaa">Title &amp; more</h1>
<p data-kapi-id="bbbbbbbb" class="lead">Text with <em>em</em>, <a href="https://x.test/?a=1&amp;b=2" title="say &quot;hi&quot;">a link</a>, <code>a &lt; b &gt; c</code> and a&nbsp;space.</p>
<ul data-kapi-id="cccccccc"><li>one</li><li>two<ol start="3"><li>three</li></ol></li></ul>
<table data-kapi-id="dddddddd"><thead><tr><th scope="col">a</th></tr></thead><tbody><tr><td colspan="2">b</td></tr></tbody></table>
<details open="" data-kapi-id="eeeeeeee"><summary>More</summary><p>Hidden <br> text <img src="a.png" alt="x"></p></details>
<blockquote data-kapi-id="ffffffff" data-kapi-state="done"><p>"Quoted" it's</p></blockquote>
<!-- a comment -->
<pre data-kapi-id="gggggggg">  indented
    code</pre>`
	if got := render(t, src); got != src {
		t.Errorf("prose did not render as its source\ngot:\n%s\nwant:\n%s", got, src)
	}
}

func TestPresetsCarryTheirName(t *testing.T) {
	for name, src := range sources(t) {
		doc, err := html.Parse(strings.NewReader("<body>" + render(t, string(src))))
		if err != nil {
			t.Fatal(err)
		}
		in, _ := parse(src)
		var walkSrc func(n *html.Node)
		want := map[string]string{}
		walkSrc = func(n *html.Node) {
			if n.Type == html.ElementNode && strings.HasPrefix(n.Data, "kapi-") {
				if id, ok := attr(n, "data-kapi-id"); ok {
					want[id] = strings.TrimPrefix(n.Data, "kapi-")
				}
			}
			if n.Type == html.ElementNode && rawPresets[n.Data] {
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walkSrc(c)
			}
		}
		for n := in.FirstChild; n != nil; n = n.NextSibling {
			walkSrc(n)
		}
		got := map[string]string{}
		var walk func(n *html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode {
				id, hasID := attr(n, "data-kapi-id")
				preset, hasPreset := attr(n, "data-kapi-preset")
				if hasPreset && hasID {
					got[id] = preset
				}
				if hasPreset && !strings.Contains(attrVal(n, "class"), "kapi-") {
					t.Errorf("%s: prose element <%s> carries data-kapi-preset=%q", name, n.Data, preset)
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		for id, preset := range want {
			if got[id] != preset {
				t.Errorf("%s: element %s rendered data-kapi-preset=%q, want %q", name, id, got[id], preset)
			}
		}
	}
}

func TestPresetExamplesRenderCleanly(t *testing.T) {
	names := map[string]bool{}
	for _, p := range Presets() {
		names[p.Name] = true
		if p.Summary == "" || p.Example == "" {
			t.Errorf("%s has no summary or example", p.Name)
		}
		out := render(t, p.Example)
		if strings.Contains(out, "kapi-error") {
			t.Errorf("%s example renders an error box:\n%s", p.Name, out)
		}
		if !strings.Contains(out, `data-kapi-preset="`+strings.TrimPrefix(p.Name, "kapi-")+`"`) {
			t.Errorf("%s example does not render the preset", p.Name)
		}
	}
	for name := range presets {
		if parents[name] == "" && !names[name] {
			t.Errorf("%s renders but is missing from Presets()", name)
		}
	}
	if len(names) != 8 {
		t.Errorf("Presets() has %d entries, the contract lists 8", len(names))
	}
}

func TestCSSCoversEveryPreset(t *testing.T) {
	css := string(CSS())
	for _, sel := range []string{".kapi-callout", ".kapi-code", ".kapi-diff-add", ".kapi-diff-del", ".kapi-diff-hunk", ".kapi-table", ".kapi-compare", ".kapi-option", ".kapi-plan", ".kapi-step", ".kapi-risk", ".kapi-question", ".kapi-diagram", ".kd-node", ".kapi-form", ".kapi-opt", ".kapi-text", ".kapi-error", ".kapi-hl .t-k{", ".kapi-hl .t-s{", ".kapi-hl .t-c1{", "#kapi-page) h1", "#kapi-page) a", "@media (max-width"} {
		if !strings.Contains(css, sel) {
			t.Errorf("CSS has no rule for %s", sel)
		}
	}
	if strings.Contains(css, "url(") || strings.Contains(css, "@import") {
		t.Error("CSS must not load anything")
	}
	a := CSS()
	a[0] = 'x'
	if CSS()[0] == 'x' {
		t.Error("CSS returns a shared buffer")
	}
}

func TestTableRefusesContentBesideTheTable(t *testing.T) {
	for _, src := range []string{
		`<kapi-table>Caption text <table><tr><td>1</td></tr></table></kapi-table>`,
		`<kapi-table><table><tr><td>1</td></tr></table> trailing words</kapi-table>`,
		`<kapi-table><table><tr><td>1</td></tr></table><p>a note</p></kapi-table>`,
		`<kapi-table><table><tr><td>1</td></tr></table><table><tr><td>2</td></tr></table></kapi-table>`,
	} {
		if out := render(t, src); !strings.Contains(out, "kapi-error") {
			t.Errorf("Render(%q) = %q, want an error box instead of dropping content", src, out)
		}
	}
	ok := "<kapi-table>\n  <!-- note -->\n  <table><tr><td>1</td></tr></table>\n</kapi-table>"
	if out := render(t, ok); strings.Contains(out, "kapi-error") {
		t.Errorf("whitespace and comments beside the table should be fine: %s", out)
	}
}
