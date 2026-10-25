package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownGolden(t *testing.T) {
	inputs, err := filepath.Glob("testdata/cases/*.html")
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no cases: %v", err)
	}
	own, _ := filepath.Glob("testdata/markdown/*.html")
	inputs = append(append(inputs, own...), "testdata/all-presets.src.html")
	for _, in := range inputs {
		name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(in), ".html"), ".src")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Markdown(src, nil)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, filepath.Join("testdata", "markdown", name+".md"), got)
		})
	}
}

func TestMarkdownEscapesTextAndKeepsEachConstructApart(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"markup in text", `<p>2 * 3 = 6, [not a link], <b>&lt;tag&gt;</b>, a_b _c_ &amp;amp; ~x~ and \ </p>`,
			`2 \* 3 = 6, \[not a link\], <strong>\<tag></strong>, a_b \_c\_ \&amp; \~x\~ and \\`},
		{"line starts", `<p># not a heading</p><p>- not a list</p><p>12. not a list either</p><p>&gt; not a quote</p>`,
			"\\# not a heading\n\n\\- not a list\n\n12\\. not a list either\n\n\\> not a quote"},
		{"hard break", `<p>one<br>two<br></p>`, "one\\\ntwo"},
		{"emphasis spaces move outside", `<p>a<em> b </em>c<strong></strong>d</p>`, "a *b* cd"},
		{"code spans", "<p><code>a`b</code> <code>`x`</code></p>", "``a`b`` `` `x` ``"},
		{"fence longer than the code's backticks", "<pre><code class=\"language-md\">```\nx\n```</code></pre>", "````md\n```\nx\n```\n````"},
		{"links", `<p><a href="https://e.com/a b(c)">x</a> <a href="https://e.com/">  </a> <a>plain</a></p>`,
			"[x](<https://e.com/a b(c)>) [https://e.com/](https://e.com/) plain"},
		{"lists", `<ol start="4"><li>four<ul><li>nested<p>para</p></li></ul></li><li>five</li></ol><ol><li>again</li></ol>`,
			"4. four\n   - nested\n\n     para\n5. five\n\n<!-- -->\n\n1. again"},
		{"a nested list that cannot interrupt a paragraph", `<ul><li>two<ol start="3"><li>three</li></ol></li></ul><ol><li>a<ol><li>b</li></ol></li></ol>`,
			"- two\n\n  3. three\n\n1. a\n   1. b"},
		{"list starts out of range", `<ol start="-3"><li>a</li></ol><ol start="12345678901"><li>b</li></ol><ol><li value="-1">c</li></ol>`,
			"0. a\n\n<!-- -->\n\n999999999. b\n\n<!-- -->\n\n0. c"},
		{"table cells", `<table><tr><th>a|b</th><th>c</th></tr><tr><td>1<br>2</td><td><p>x</p><p>y</p></td></tr></table>`,
			"| a\\|b | c |\n| --- | --- |\n| 1<br>2 | x<br>y |"},
		{"quote holds blocks", `<blockquote><p>a</p><ul><li>b</li></ul></blockquote>`, "> a\n>\n> - b"},
		{"definition list", `<dl><dt>Term</dt><dd>Means this</dd><dt>Other</dt><dd><p>One</p><p>Two</p></dd></dl>`,
			"- **Term**: Means this\n- **Other**\n\n  One\n\n  Two"},
		{"badge states", `<p><kapi-badge state="done"></kapi-badge> <kapi-badge state="waiting">next</kapi-badge> <kapi-badge tone="red">x</kapi-badge></p>`,
			"`Done` `Waiting: next` `x`"},
		{"plan states", `<kapi-plan><kapi-step state="done">a</kapi-step><kapi-step>b</kapi-step></kapi-plan>`, "1. **Done:** a\n2. b"},
		{"a label before block content goes on its own line",
			`<kapi-plan><kapi-risk><kapi-code lang="go">x</kapi-code></kapi-risk><kapi-step state="done"><ul><li>a</li></ul></kapi-step></kapi-plan>` +
				`<kapi-checklist><kapi-task><ul><li>b</li></ul></kapi-task><kapi-task done><p>c</p><p>d</p></kapi-task></kapi-checklist>`,
			"- **Risk**\n\n  ```go\n  x\n  ```\n\n1. **Done**\n   - a\n\n- [ ] **Task**\n  - b\n- [x] c\n\n  d"},
		{"callouts use GitHub alerts only for a note or a warning",
			`<kapi-callout kind="warning">w</kapi-callout><kapi-callout kind="success">s</kapi-callout><kapi-callout kind="key">k</kapi-callout>` +
				`<kapi-callout kind="danger"><ul><li>d</li></ul></kapi-callout>`,
			"> [!WARNING]\n> w\n\n> **Success:** s\n\n> **Key point:** k\n\n> **Danger**\n>\n> - d"},
		{"attribute text cannot break out", "<kapi-code file=\"a\n\n<img src=x onerror=alert(1)>\">x</kapi-code><kapi-diff file=\"b\n\n<iframe>\">@@ -1 +1 @@\n-a\n+b</kapi-diff>" +
			"<kapi-details summary=\"s\n\n<b>x</b>\">y</kapi-details>",
			"`a <img src=x onerror=alert(1)>`\n\n```\nx\n```\n\n`b <iframe>`\n\n```diff\n@@ -1 +1 @@\n-a\n+b\n```\n\n<details>\n<summary>s &lt;b&gt;x&lt;/b&gt;</summary>\n\ny\n\n</details>"},
		{"an option value cannot start a block", `<kapi-form name="f"><kapi-choice name="c">Q<kapi-opt value="a&#10;&#10;# H"></kapi-opt></kapi-choice></kapi-form>`,
			"Q\n\n- [ ] a # H"},
		{"links keep newlines out", `<p><a href="https://e.com/a&#10;&#10;~~~">x</a> <img src="https://e.com/b&#9;c.png" alt="i"></p>`,
			"[x](https://e.com/a%0A%0A~~~) ![i](https://e.com/b%09c.png)"},
		{"code in a table cell", "<table><tr><th>c</th></tr><tr><td><kapi-code lang=\"go\">a := 1\nb | 2</kapi-code></td></tr></table>",
			"| c |\n| --- |\n| `a := 1`<br>`b \\| 2` |"},
		{"emphasis that markers cannot express", `<p>a<em>(b)</em>c x<strong>"y"</strong>z <em><em>n</em></em> p<del>.q</del></p>`,
			`a<em>(b)</em>c x<strong>"y"</strong>z <em>*n*</em> p<del>.q</del>`},
		{"backticks and other line starts", "<p>a `b` c</p><p>+ x</p><p>| y</p><p>===</p>", "a \\`b\\` c\n\n\\+ x\n\n\\| y\n\n\\==="},
		{"a list after a block that ends in one", `<kapi-plan><kapi-step>a</kapi-step><kapi-risk>r</kapi-risk></kapi-plan><ul><li>b</li></ul>`,
			"1. a\n\n- **Risk:** r\n\n<!-- -->\n\n- b"},
		{"heading ending in a hash", `<h2>Issue #</h2><h3>C#</h3>`, "## Issue \\#\n\n### C#"},
		{"a language that would break the fence", "<kapi-code lang=\"go x\">a</kapi-code><kapi-code file=\"a.g`o\">b</kapi-code>", "```\na\n```\n\n``a.g`o``\n\n```\nb\n```"},
		{"dropped content", `<p>keep<script>alert(1)</script></p><style>p{}</style><svg><text>no</text></svg>`, "keep"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Markdown([]byte(c.src), nil)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want+"\n" {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestEveryPresetHasAGoldenCase(t *testing.T) {
	inputs, err := filepath.Glob("testdata/cases/*.html")
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, in := range inputs {
		b, err := os.ReadFile(in)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	for name := range presets {
		if !strings.Contains(all.String(), "<"+name+" ") && !strings.Contains(all.String(), "<"+name+">") {
			t.Errorf("no case in testdata/cases uses <%s>", name)
		}
	}
}

func TestMarkdownPassesEveryImageSourceThroughTheCallback(t *testing.T) {
	var seen []string
	got, err := Markdown([]byte(`<p><img src="./a.files/x.png" alt="a]b"></p><figure><img src="https://e.com/y.png"><figcaption>Cap</figcaption></figure>`), func(src string) string {
		seen = append(seen, src)
		return "images/" + filepath.Base(src)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "![a\\]b](images/x.png)\n\n![](images/y.png)\n\n*Cap*\n"
	if string(got) != want || len(seen) != 2 {
		t.Errorf("got %q (callback saw %v), want %q", got, seen, want)
	}
}
