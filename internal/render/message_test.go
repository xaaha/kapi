package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func TestMessageFormatsMarkdown(t *testing.T) {
	got := message(t, "All 8 checks pass on `f6cf9a4b`.\n\n## Left\n\n1. A teammate review\n2. **Retarget** to main\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc main() {}\n```\n\n> quoted\n\n- [ ] task\n\n~~old~~ https://example.com\nnext line")
	for _, want := range []string{
		"<code>f6cf9a4b</code>",
		"<h2>Left</h2>",
		"<ol>", "<li>A teammate review</li>", "<strong>Retarget</strong>",
		`<div class="kapi-table">`, "<th>a</th>",
		`<figure class="kapi-code">`, `<span class="t-kd">func</span>`,
		"<blockquote>",
		`<input disabled="" type="checkbox"`,
		"<del>old</del>",
		`<a href="https://example.com" target="_blank" rel="noopener noreferrer">https://example.com</a><br/>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "data-kapi-") {
		t.Errorf("page-only attributes leaked into a message:\n%s", got)
	}
}

func TestMessageNeverPassesScriptsHandlersOrJavascriptLinks(t *testing.T) {
	for _, src := range []string{
		"<script>alert(1)</script>",
		`<img src=x onerror="alert(1)">`,
		`<a href="javascript:alert(1)" onclick="alert(1)">x</a>`,
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"![x](javascript:alert(1))",
		`<div onmouseover="alert(1)">hover</div>`,
		"<iframe src=https://example.com></iframe>",
		"<style>body{display:none}</style>",
		"<!-- <script>alert(1)</script> -->",
		"[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)",
		"![x](data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=)",
		"[x](vbscript:msgbox(1))",
		"[x](&#106;avascript:alert(1))",
		"[x]: javascript:alert(1)\n\n[x]",
		`<a href="x" style="position:fixed;inset:0">x</a>`,
		"`</div><script>alert(1)</script>`",
		"<svg onload=alert(1)>",
		"<input type=checkbox onclick=alert(1)>",
	} {
		got := message(t, src)
		nodes, err := html.ParseFragment(strings.NewReader(got), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
		if err != nil {
			t.Fatal(err)
		}
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			switch {
			case n.Type == html.CommentNode:
				t.Errorf("%q left a comment: %s", src, got)
			case n.Type == html.ElementNode && dropped[n.Data]:
				t.Errorf("%q let a %s element through: %s", src, n.Data, got)
			}
			for _, a := range n.Attr {
				if a.Key == "style" || (urlAttrs[a.Key] && !safeURL(a.Val, n.Data == "img")) {
					t.Errorf("%q let %s=%q through: %s", src, a.Key, a.Val, got)
				}
				if strings.HasPrefix(a.Key, "on") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
					t.Errorf("%q let %s=%q through: %s", src, a.Key, a.Val, got)
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		for _, n := range nodes {
			walk(n)
		}
	}
}

func TestMessageShowsADroppedLinkAsItsText(t *testing.T) {
	if got := message(t, "[click **here**](javascript:alert(1)) now"); got != "<p>click <strong>here</strong> now</p>" {
		t.Errorf("got %q", got)
	}
}

func TestMessageKeepsPlainTextReadable(t *testing.T) {
	if got := message(t, "Status: done.\nRun kapi say <page> <text> next."); got != "<p>Status: done.<br/>\nRun kapi say &lt;page&gt; &lt;text&gt; next.</p>" {
		t.Errorf("got %q", got)
	}
	if got := message(t, "<div>\nshown as typed\n</div>"); got != "<p>&lt;div&gt;<br/>\nshown as typed<br/>\n&lt;/div&gt;</p>" {
		t.Errorf("got %q", got)
	}
}

func message(t *testing.T, src string) string {
	t.Helper()
	out, err := Message(src)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
