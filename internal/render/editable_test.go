package render

import (
	"bytes"
	"os"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func TestEveryPresetDeclaresItsText(t *testing.T) {
	for name := range presets {
		if _, ok := textParts[name]; !ok {
			t.Errorf("%s does not say which of its parts are text; add it to textParts", name)
		}
	}
	for name := range textParts {
		if _, ok := presets[name]; !ok {
			t.Errorf("textParts names %s, which is not a preset", name)
		}
	}
	for _, p := range Presets() {
		if _, ok := textParts[p.Name]; !ok {
			t.Errorf("the catalog lists %s, which textParts does not declare", p.Name)
		}
	}
}

func TestEveryPresetMarksTheTextItDeclares(t *testing.T) {
	src, err := os.ReadFile("testdata/all-presets.src.html")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(src)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := html.ParseFragment(bytes.NewReader(out), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string][]*html.Node{}
	var presetsOut []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if id, ok := attr(n, textAttr); ok {
				marked[id] = append(marked[id], n)
			}
			if _, ok := attr(n, "data-kapi-preset"); ok && attrVal(n, "data-kapi-id") != "" {
				presetsOut = append(presetsOut, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	seen := map[TextPart]bool{}
	for _, n := range presetsOut {
		name, id := "kapi-"+attrVal(n, "data-kapi-preset"), attrVal(n, "data-kapi-id")
		kind := TextOf(name)
		seen[kind] = true
		got := marked[id]
		switch kind {
		case NoText:
			if len(got) != 0 {
				t.Errorf("%s #%s declares no text but marks %d parts", name, id, len(got))
			}
		case BodyText:
			if len(got) != 1 || got[0] == n {
				t.Errorf("%s #%s declares a body but marks %d parts", name, id, len(got))
			}
		case RawText:
			if _, raw := attr(n, rawAttr); len(got) != 1 || got[0] != n || !raw {
				t.Errorf("%s #%s declares raw text but its outer element is not the one raw part", name, id)
			}
		case CellText:
			if len(got) == 0 {
				t.Errorf("%s #%s declares cells but marks none", name, id)
			}
			for _, c := range got {
				if c.Data != "td" && c.Data != "th" || attrVal(c, cellAttr) == "" {
					t.Errorf("%s #%s marks a <%s> that is not a numbered cell", name, id, c.Data)
				}
			}
		}
	}
	for _, kind := range []TextPart{NoText, BodyText, CellText, RawText} {
		if !seen[kind] {
			t.Errorf("the sample has no preset whose text is of kind %d", kind)
		}
	}
}

func TestTextMarksInTheSourceAreNotTrusted(t *testing.T) {
	out, err := Render([]byte(`<p data-kapi-id="aaaaaaaa" data-kapi-text="bbbbbbbb">a</p><kapi-plan data-kapi-id="cccccccc" data-kapi-text="cccccccc" data-kapi-raw=""><kapi-step data-kapi-id="dddddddd">x</kapi-step></kapi-plan>`))
	if err != nil {
		t.Fatal(err)
	}
	want := `<p data-kapi-id="aaaaaaaa" data-kapi-text="aaaaaaaa">a</p><div class="kapi-plan" data-kapi-preset="plan" data-kapi-id="cccccccc"><div class="kapi-plan-item kapi-step kapi-tone-purple" data-kapi-preset="step" data-kapi-id="dddddddd"><span class="kapi-plan-mark"></span><div class="kapi-plan-body" data-kapi-text="dddddddd">x</div></div></div>`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}
