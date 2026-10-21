package render

import (
	"bytes"
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
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(out), body)
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
