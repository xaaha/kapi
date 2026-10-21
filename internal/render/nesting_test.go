package render

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func literalTree(t *testing.T, out string) string {
	t.Helper()
	root := &html.Node{Type: html.ElementNode, Data: "body"}
	stack := []*html.Node{root}
	z := html.NewTokenizer(strings.NewReader(out))
	afterPre := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				t.Fatal(z.Err())
			}
			break
		}
		tok := z.Token()
		top := stack[len(stack)-1]
		if afterPre && tt == html.TextToken {
			tok.Data = strings.TrimPrefix(tok.Data, "\n")
		}
		afterPre = tt == html.StartTagToken && tok.Data == "pre"
		switch tt {
		case html.TextToken:
			top.AppendChild(&html.Node{Type: html.TextNode, Data: tok.Data})
		case html.CommentToken:
			top.AppendChild(&html.Node{Type: html.CommentNode, Data: tok.Data})
		case html.StartTagToken, html.SelfClosingTagToken:
			n := &html.Node{Type: html.ElementNode, Data: tok.Data, Attr: tok.Attr}
			top.AppendChild(n)
			if tt == html.StartTagToken && !voidTags[tok.Data] && tok.Data != "input" {
				stack = append(stack, n)
			}
		case html.EndTagToken:
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].Data == tok.Data {
					stack = stack[:i]
					break
				}
			}
		}
	}
	return serialize(t, root)
}

func browserTree(t *testing.T, out string) string {
	t.Helper()
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(out), body)
	if err != nil {
		t.Fatal(err)
	}
	root := &html.Node{Type: html.ElementNode, Data: "body"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	return serialize(t, root)
}

func serialize(t *testing.T, root *html.Node) string {
	t.Helper()
	var b bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			t.Fatal(err)
		}
	}
	return strings.ToLower(b.String())
}

func assertParseStable(t *testing.T, name, out string) {
	t.Helper()
	if lit, dom := literalTree(t, out), browserTree(t, out); lit != dom {
		t.Errorf("%s: a browser parses the output into a different tree\noutput:  %s\nbrowser: %s", name, lit, dom)
	}
}

func TestRenderOutputIsParseStable(t *testing.T) {
	for name, src := range sources(t) {
		assertParseStable(t, name, render(t, string(src)))
	}
}

var nestedProse = map[string]string{
	"div":        `<div data-kapi-id="wrapaaaa">%s</div>`,
	"li":         `<ul data-kapi-id="wrapaaaa"><li>%s</li></ul>`,
	"td":         `<table data-kapi-id="wrapaaaa"><tbody><tr><td>%s</td></tr></tbody></table>`,
	"blockquote": `<blockquote data-kapi-id="wrapaaaa">%s</blockquote>`,
	"details":    `<details data-kapi-id="wrapaaaa"><summary>s</summary>%s</details>`,
	"section":    `<section data-kapi-id="wrapaaaa"><h2>t</h2>%s</section>`,
	"p":          `<p data-kapi-id="wrapaaaa">before %s after</p>`,
	"em in p":    `<p data-kapi-id="wrapaaaa">before <em>x %s y</em> after</p>`,
	"a in p":     `<p data-kapi-id="wrapaaaa"><a href="#x">link %s text</a></p>`,
	"h2":         `<h2 data-kapi-id="wrapaaaa">title %s</h2>`,
	"span":       `<div><span data-kapi-id="wrapaaaa">%s</span></div>`,
	"callout":    `<kapi-callout data-kapi-id="wrapaaaa">%s</kapi-callout>`,
	"step":       `<kapi-plan><kapi-step data-kapi-id="wrapaaaa">%s</kapi-step></kapi-plan>`,
	"option":     `<kapi-compare><kapi-option title="o" data-kapi-id="wrapaaaa">%s</kapi-option></kapi-compare>`,
}

var nestedPresets = map[string]string{
	"callout": `<kapi-callout kind="key" data-kapi-id="presaaaa"><p>inner</p></kapi-callout>`,
	"code":    `<kapi-code lang="go" data-kapi-id="presaaaa">x := 1</kapi-code>`,
	"table":   `<kapi-table sort="on" data-kapi-id="presaaaa"><table><tr><td>1</td></tr></table></kapi-table>`,
	"diagram": `<kapi-diagram data-kapi-id="presaaaa">a -> b</kapi-diagram>`,
	"form": `<kapi-form name="f" data-kapi-id="presaaaa"><kapi-choice name="c"><kapi-opt value="v">V</kapi-opt></kapi-choice>` +
		`<kapi-text name="t">T</kapi-text></kapi-form>`,
}

func TestPresetsNestedInProse(t *testing.T) {
	for wrapName, wrap := range nestedProse {
		for presetName, preset := range nestedPresets {
			name := presetName + " in " + wrapName
			src := strings.Replace(wrap, "%s", preset, 1)
			out := render(t, src)
			assertParseStable(t, name, out)
			body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
			nodes, err := html.ParseFragment(strings.NewReader(out), body)
			if err != nil {
				t.Fatal(err)
			}
			var found *html.Node
			for _, n := range nodes {
				if found == nil {
					found = findAttr(n, "data-kapi-id", "presaaaa")
				}
			}
			if found == nil || attrVal(found, "data-kapi-preset") != presetName {
				t.Errorf("%s: no element with data-kapi-preset=%q:\n%s", name, presetName, out)
				continue
			}
			if presetName == "form" {
				if found.Data != "form" || countTag(found, "input") != 1 || countTag(found, "textarea") != 1 || countTag(found, "button") != 1 {
					t.Errorf("%s: the browser's form does not hold its controls:\n%s", name, out)
				}
			}
			block, err := Block([]byte(src), "presaaaa")
			if err != nil || !strings.Contains(out, string(block)) {
				t.Errorf("%s: Block = %q, %v; not a fragment of Render", name, block, err)
			}
		}
	}
}

func TestFormInsideFormIsAnError(t *testing.T) {
	out := render(t, `<kapi-form name="outer"><kapi-text name="a">a</kapi-text><div><kapi-form name="inner" data-kapi-id="innerfrm"><kapi-text name="b">b</kapi-text></kapi-form></div></kapi-form>`)
	assertParseStable(t, "nested form", out)
	if strings.Count(out, "<form") != 1 || !strings.Contains(out, `kapi-error" data-kapi-preset="form"`) {
		t.Errorf("nested kapi-form should render as an error box:\n%s", out)
	}
}

func findAttr(n *html.Node, key, val string) *html.Node {
	if n.Type == html.ElementNode && attrVal(n, key) == val {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findAttr(c, key, val); f != nil {
			return f
		}
	}
	return nil
}

func countTag(n *html.Node, tag string) int {
	count := 0
	if n.Type == html.ElementNode && n.Data == tag {
		count++
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		count += countTag(c, tag)
	}
	return count
}
