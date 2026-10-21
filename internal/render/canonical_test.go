package render

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var messySources = []string{
	`<p class=lead id='intro' data-kapi-id=aaaaaaaa>Quotes 'single' and "double" &amp; &lt;tags&gt; &#39; &#x27; &copy; &nbsp;x</p>`,
	`<p>line<br/>break<br>again<hr/></p>`,
	`<details open><summary>s</summary>x</details><ol reversed start=3><li>a<li>b</ol>`,
	`<table><tr><td>implied tbody<td>second</table>`,
	`<p>para <div>splits the paragraph</div> tail</p>`,
	`<P CLASS="x">Upper case tags</P><UL><LI>item</UL>`,
	`<p>unclosed <em>emphasis <strong>and strong</p><p>next`,
	`<a href="https://e.test/?a=1&b=2">raw ampersand</a> <img src=a.png alt=x>`,
	`text at the top level <!-- comment --> <b>bold</b>`,
	`<pre>` + "\n\nleading newlines</pre>",
	`<p onclick="x()" style="color:red" data-kapi-preset="code">sanitized</p><script>alert(1)</script>`,
	`<kapi-callout kind=warning onclick="x()" data-kapi-id=bbbbbbbb><p>inside</p><script>x</script></kapi-callout>`,
	`<kapi-code lang=go>if a &lt; b && c > d { return "<div>" }</kapi-code>`,
	`<kapi-code><div>unescaped</div><!-- c --></kapi-code>`,
	`<kapi-diagram type=sequence>a -> b: hi &amp; bye` + "\r\n" + `b --> a</kapi-diagram>`,
	`<p>before <kapi-callout>moved out</kapi-callout> after</p>`,
	`<p data-kapi-id=cccccccc><em>x <kapi-form name=f><kapi-text name=t>t</kapi-text></kapi-form> y</em></p>`,
	`<kapi-form name=f><kapi-choice name=c multiple>Q<kapi-opt value=1>One</kapi-opt></kapi-choice></kapi-form>`,
	`<kapi-unknown foo=bar data-x=1>child</kapi-unknown>`,
	`<kapi-table sort=on><table><tr><th>h<tr><td>1</table></kapi-table>`,
	`<ul><li>one<kapi-code>x</kapi-code></li></ul>`,
	`<svg><a href="javascript:x"><text>svg</text></a></svg><math><mi>m</mi></math>`,
	`<font color=red><div>font around a block</div></font>`,
	`<a href="#x"><div>block in a link</div></a>`,
	`<custom-element><p>unknown wrapper</p></custom-element>`,
	`<b data-kapi-id="dupedupe">bold<div>block</b>after</div><i data-kapi-id="iiiiiiii"><p>x<p>y</i>`,
}

func canonical(t *testing.T, src string) string {
	t.Helper()
	out, err := Canonical([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func canonicalSources(t *testing.T) map[string]string {
	t.Helper()
	all := map[string]string{}
	for name, src := range sources(t) {
		all[name] = string(src)
	}
	for i, src := range messySources {
		all["messy "+string(rune('a'+i))] = src
	}
	return all
}

func proseOnly(t *testing.T, out string, isPreset func(*html.Node) bool) string {
	t.Helper()
	marked, bodies := markRawBodies([]byte(out))
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(marked), body)
	if err != nil {
		t.Fatal(err)
	}
	var replace func(n *html.Node)
	replace = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode && isPreset(c) {
				n.InsertBefore(&html.Node{Type: html.CommentNode, Data: "preset"}, c)
				n.RemoveChild(c)
			} else {
				replace(c)
			}
			c = next
		}
	}
	root := &html.Node{Type: html.ElementNode, Data: "body"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	restoreRawBodies(root, bodies)
	replace(root)
	var b bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}

func renderedPreset(n *html.Node) bool { _, ok := attr(n, "data-kapi-preset"); return ok }

func sourcePreset(n *html.Node) bool { return strings.HasPrefix(n.Data, "kapi-") }

func checkCanonical(t *testing.T, name, src string) {
	t.Helper()
	c := canonical(t, src)
	if again := canonical(t, c); again != c {
		t.Errorf("%s: Canonical is not idempotent\nonce:  %s\ntwice: %s", name, c, again)
	}
	r := render(t, src)
	if rc := render(t, c); rc != r {
		t.Errorf("%s: Render(Canonical(src)) differs from Render(src)\nsrc:       %s\ncanonical: %s\nrender:    %s\nviaCanon:  %s", name, src, c, r, rc)
	}
	if pr, pc := proseOnly(t, r, renderedPreset), proseOnly(t, c, sourcePreset); pr != pc {
		t.Errorf("%s: prose in Render differs from prose in Canonical\nrender:    %s\ncanonical: %s", name, pr, pc)
	}
	assertParseStable(t, name, r)
	for _, out := range []string{c, r} {
		seen := map[string]bool{}
		for _, m := range kapiIDAttr.FindAllStringSubmatch(out, -1) {
			if seen[m[1]] {
				t.Errorf("%s: data-kapi-id %q appears twice:\n%s", name, m[1], out)
			}
			seen[m[1]] = true
		}
	}
}

var kapiIDAttr = regexp.MustCompile(`data-kapi-id="([^"]*)"`)

func TestCanonicalKeepsTheFirstOfDuplicateIDs(t *testing.T) {
	cases := map[string]string{
		`<b data-kapi-id="xxxxxxxx">bold<div>block</div>tail</b>`:                                                      `xxxxxxxx`,
		`<p data-kapi-id="yyyyyyyy">one</p><p data-kapi-id="yyyyyyyy">two</p>`:                                         `yyyyyyyy`,
		`<kapi-callout data-kapi-id="zzzzzzzz">a</kapi-callout><kapi-callout data-kapi-id="zzzzzzzz">b</kapi-callout>`: `zzzzzzzz`,
	}
	for src, id := range cases {
		c := canonical(t, src)
		if n := strings.Count(c, `data-kapi-id="`+id+`"`); n != 1 {
			t.Errorf("Canonical(%q) = %q has the id %d times, want 1", src, c, n)
		}
		if strings.Index(c, id) > strings.Index(c, ">") {
			t.Errorf("Canonical(%q) = %q moved the id off the first element", src, c)
		}
	}
}

func TestCanonical(t *testing.T) {
	for name, src := range canonicalSources(t) {
		checkCanonical(t, name, src)
	}
}

func TestCanonicalProseIsRender(t *testing.T) {
	for _, src := range messySources {
		if strings.Contains(src, "kapi-") {
			continue
		}
		if c, r := canonical(t, src), render(t, src); c != r {
			t.Errorf("Canonical(%q) = %q, Render = %q", src, c, r)
		}
	}
}

func TestCanonicalKeepsPresetSource(t *testing.T) {
	cases := map[string]string{
		`<kapi-diagram>a -> b: x &amp; y</kapi-diagram>`:                     `<kapi-diagram>a -> b: x &amp; y</kapi-diagram>`,
		`<kapi-code lang=go file="a.go" lines=3-4>a &lt; b</kapi-code>`:      `<kapi-code lang="go" file="a.go" lines="3-4">a &lt; b</kapi-code>`,
		`<kapi-callout kind=key onclick=x data-kapi-id=k1>hi</kapi-callout>`: `<kapi-callout kind="key" data-kapi-id="k1">hi</kapi-callout>`,
		`<kapi-choice name=c multiple onclick=x>q</kapi-choice>`:             `<kapi-choice name="c" multiple="">q</kapi-choice>`,
	}
	for src, want := range cases {
		if got := canonical(t, src); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", src, got, want)
		}
	}
}

func FuzzCanonical(f *testing.F) {
	for _, src := range messySources {
		f.Add(src)
	}
	for _, p := range Presets() {
		f.Add(p.Example)
	}
	f.Fuzz(func(t *testing.T, src string) {
		checkCanonical(t, "fuzz", src)
	})
}

func TestDroppedNamesWhatCanonicalRemoves(t *testing.T) {
	cases := map[string]string{
		"<!doctype html><html lang=\"en\"><head><title>t</title><style>p{}</style></head><body class=\"x\">" +
			`<script>a()</script><p onclick="b()" style="color:red">hi</p><script src="c.js"></script></body></html>`: "2 script, 1 body, 1 doctype, 1 head, 1 html, 1 style, 1 title, 4 attributes",
		`<kapi-callout kind="note" onclick="x()">a</kapi-callout><custom-tag>text kept</custom-tag>`: "1 custom-tag, 1 attribute",
		`<svg><circle r="1"></circle></svg><p>x</p>`:                                                 "1 svg",
	}
	for src, want := range cases {
		r, err := Dropped([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if r.Empty() || r.String() != want {
			t.Errorf("Dropped(%q) = %q, want %q", src, r.String(), want)
		}
	}
	for _, src := range []string{
		`<h1 data-kapi-id="aaaaaaaa">Plan</h1><p class=lead>Text &amp; <a href="https://e.test/">link</a></p>`,
		`<p data-kapi-id="aaaaaaaa">before <kapi-callout data-kapi-id="bbbbbbbb">kept</kapi-callout> after</p>`,
		`<p>one</p><p>two</p><table><tr><td>implied tbody</td></tr></table><br/>`,
		`<kapi-code lang="go"><div>raw body</div></kapi-code><!-- comment -->`,
	} {
		if r, err := Dropped([]byte(src)); err != nil || !r.Empty() {
			t.Errorf("Dropped(%q) = %q, %v; want nothing dropped", src, r.String(), err)
		}
	}
}

func TestCodeBodiesAreRawSource(t *testing.T) {
	bodies := []string{
		"\nstd::vector<int> v;\nif (a < b && c > d) { v.push_back(1); }\n",
		"<table><tr><td data-kapi-id=\"aaaaaaaa\">cell</td></tr></table>",
		"x &lt; y &amp; <kapi-callout>not a preset</kapi-callout> <!-- c --> </pre>",
		"@@ -1 +1 @@\n-std::map<K, V> m;\n+std::unordered_map<K, V> m;",
	}
	for _, tag := range []string{"kapi-code", "kapi-diff"} {
		for _, body := range bodies {
			src := `<p>before</p><` + tag + ` lang="cpp">` + body + `</` + tag + `><p>after</p>`
			c := canonical(t, src)
			if !strings.Contains(c, ">"+body+"</"+tag+">") {
				t.Errorf("Canonical lost the %s body bytes\nbody: %q\ngot:  %q", tag, body, c)
			}
			out := render(t, src)
			text := textContent(t, out)
			for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
				line = strings.TrimLeft(line, "+- @")
				if !strings.Contains(text, strings.TrimSpace(line)) {
					t.Errorf("Render of %s does not show %q:\n%s", tag, line, out)
				}
			}
			if strings.Contains(out, "kapi-error") || strings.Contains(out, `data-kapi-id="aaaaaaaa"`) {
				t.Errorf("code body was parsed as markup:\n%s", out)
			}
			if _, err := Block([]byte(src), "aaaaaaaa"); !errors.Is(err, ErrNotFound) {
				t.Errorf("Block found an id inside a %s body", tag)
			}
		}
	}
}

func TestMaskRawBodiesKeepsOffsets(t *testing.T) {
	src := []byte(`<kapi-code>a<b>c</b></kapi-code><p data-kapi-id="x">y</p><KAPI-DIFF>-<i>
+</i></Kapi-Diff >tail`)
	m := MaskRawBodies(src)
	if len(m) != len(src) || bytes.Count(m, []byte("<")) != 6 {
		t.Errorf("MaskRawBodies(%q) = %q; want the same length and only the real tags left", src, m)
	}
}

func textContent(t *testing.T, out string) string {
	t.Helper()
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(out), body)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return b.String()
}

func TestCountBlocksAndUnwraps(t *testing.T) {
	cases := map[string]int{
		`<p>one</p>`: 1,
		`<p>a <kapi-callout>c</kapi-callout> b</p>`: 3,
		`<script>x</script>`:                        0,
		`<custom-tag><p>a</p><p>b</p></custom-tag>`: 2,
		`<b>bold<div>block</b>after</div>`:          2,
		"text only":                                 0,
	}
	for src, want := range cases {
		if got, err := CountBlocks([]byte(src)); err != nil || got != want {
			t.Errorf("CountBlocks(%q) = %d, %v; want %d", src, got, err, want)
		}
	}
	for tag, want := range map[string]bool{"custom-tag": true, "font": true, "p": false, "script": false, "svg": false, "kapi-x": false, "table": false} {
		if Unwraps(tag) != want {
			t.Errorf("Unwraps(%q) = %v", tag, !want)
		}
	}
}

func TestUnlabeledCountsBlocksWithoutAnID(t *testing.T) {
	cases := map[string]int{
		`<p data-kapi-id="a">x</p><p data-kapi-id="b">y</p>`:                          0,
		`<p data-kapi-id="a">x <kapi-callout data-kapi-id="b">c</kapi-callout> y</p>`: 1,
		`<p>x</p><script>s()</script>`:                                                1,
		`<p data-kapi-id="a">x</p><p data-kapi-id="a">copy</p>`:                       1,
		`<p data-kapi-id="a">a<div data-kapi-id="b">b</div></p>`:                      1,
	}
	for src, want := range cases {
		if got, err := Unlabeled([]byte(src)); err != nil || got != want {
			t.Errorf("Unlabeled(%q) = %d, %v; want %d", src, got, err, want)
		}
	}
}

func TestCodeBodiesKeepEveryByte(t *testing.T) {
	for _, body := range []string{"a\r\nb\x00c", "\r\n\r\nx\ry\n", "tab\tthen \ufffd and \ue000 marker"} {
		for _, tag := range []string{"kapi-code", "kapi-diff"} {
			src := "<p>x <" + tag + ">" + body + "</" + tag + "> y</p><table><" + tag + ">" + body + "</" + tag + "></table>"
			c := canonical(t, src)
			if n := strings.Count(c, ">"+body+"</"+tag+">"); n != 2 {
				t.Errorf("Canonical kept the %s body %q intact %d of 2 times:\n%q", tag, body, n, c)
			}
		}
	}
}
