package render

import (
	"bytes"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var renderTags = set(
	"a", "abbr", "article", "aside", "b", "blockquote", "br", "button", "caption", "cite", "code", "col", "colgroup",
	"dd", "del", "details", "dfn", "div", "dl", "dt", "em", "fieldset", "figcaption", "figure", "footer", "form",
	"h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "i", "img", "input", "ins", "kbd", "label", "legend", "li",
	"main", "mark", "nav", "ol", "option", "p", "pre", "q", "s", "samp", "section", "select", "small", "span", "strong",
	"sub", "summary", "sup", "table", "tbody", "td", "textarea", "tfoot", "th", "thead", "time", "tr", "u", "ul", "var", "wbr",
)

var renderVoidTags = set("br", "col", "hr", "img", "input", "wbr")

var renderAttrs = map[string]map[string]bool{
	"a":        set("href", "target"),
	"button":   set("type", "disabled", "name", "value"),
	"col":      set("span"),
	"colgroup": set("span"),
	"details":  set("open"),
	"img":      set("src", "alt", "width", "height", "loading"),
	"input":    set("type", "name", "value", "placeholder", "checked", "disabled", "min", "max", "step"),
	"label":    set("for"),
	"li":       set("value"),
	"ol":       set("start", "reversed", "type"),
	"option":   set("value", "selected", "disabled"),
	"select":   set("name", "multiple", "disabled"),
	"td":       set("colspan", "rowspan", "headers"),
	"textarea": set("name", "placeholder", "disabled", "rows", "cols"),
	"th":       set("colspan", "rowspan", "headers", "scope", "abbr"),
	"time":     set("datetime"),
}

var svgTags = set("svg", "g", "path", "circle", "ellipse", "line", "polyline", "polygon", "rect", "text", "tspan", "defs", "clipPath", "linearGradient", "radialGradient", "stop")
var svgAttrs = set("viewBox", "width", "height", "x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "d", "points", "fill", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "opacity", "transform", "gradientUnits", "offset", "stop-color", "stop-opacity", "clip-path", "role")

var importCSS = regexp.MustCompile(`(?is)@import\s+[^;]+;?`)

const renderFrameCSS = `
* { box-sizing: border-box; }
html { color-scheme: light dark; background: var(--kapi-bg); }
body { margin: 0; padding: 24px; background: var(--kapi-bg); color: inherit; font: 15px/1.55 var(--kapi-font); overflow-wrap: break-word; }
h1, h2, h3, h4, h5, h6 { margin: 0 0 .65em; color: inherit; font-family: var(--kapi-font-body); line-height: 1.12; letter-spacing: -.02em; }
h1 { font-size: clamp(2rem, 5vw, 3rem); } h2 { font-size: 1.65rem; } h3 { font-size: 1.25rem; }
p, ul, ol, table, blockquote, figure { margin: 0 0 1rem; }
a { color: var(--kapi-blue-text); }
button, input, select, textarea { font: inherit; color: inherit; }
button, input, select, textarea { border: 1px solid var(--kapi-border-strong); border-radius: var(--kapi-radius-sm); background: var(--kapi-surface); }
button { padding: .5rem .9rem; cursor: default; }
input, select, textarea { padding: .5rem .65rem; }
img, svg { max-width: 100%; height: auto; }
figure { margin-inline: 0; }
figcaption { margin-top: .4rem; color: var(--kapi-muted); font-size: .85rem; }
table { width: 100%; border-collapse: collapse; }
th, td { padding: .6rem .75rem; border-bottom: 1px solid var(--kapi-border); text-align: left; }
th { color: var(--kapi-muted); font-size: .75rem; text-transform: uppercase; }
code { font-family: var(--kapi-mono); }
:not(pre) > code { padding: .1em .3em; border: 1px solid var(--kapi-border); border-radius: 5px; background: var(--kapi-subtle); }
`

func renderRich(b *bytes.Buffer, n *html.Node) error {
	title := strings.TrimSpace(attrVal(n, "title"))
	if title == "" {
		title = "HTML preview"
	}
	body := sanitizeRich(rawText(n))
	var doc bytes.Buffer
	doc.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`)
	doc.WriteString(`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; font-src 'self'; form-action 'none'; base-uri 'none'">`)
	doc.WriteString("<style>")
	doc.Write(themeCSS)
	doc.WriteString(renderFrameCSS)
	doc.WriteString("</style></head><body>")
	doc.WriteString(body)
	doc.WriteString("</body></html>")

	writeStart(b, "figure", outerAttrs(n, "kapi-render"))
	b.WriteString(`<figcaption class="kapi-render-title">`)
	writeText(b, title)
	b.WriteString(`</figcaption>`)
	writeStart(b, "iframe", kv("class", "kapi-render-frame", "title", title, "sandbox", "allow-same-origin", "loading", "lazy", "srcdoc", doc.String()))
	writeEnd(b, "iframe")
	writeEnd(b, "figure")
	return nil
}

func sanitizeRich(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return escText(src)
	}
	body := findTag(doc, "body")
	if body == nil {
		body = doc
	}
	var b bytes.Buffer
	if head := findTag(doc, "head"); head != nil {
		for c := head.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "style" {
				writeRich(&b, c, false)
			}
		}
	}
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		writeRich(&b, c, false)
	}
	return b.String()
}

func findTag(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findTag(c, tag); found != nil {
			return found
		}
	}
	return nil
}

func writeRich(b *bytes.Buffer, n *html.Node, inSVG bool) {
	switch n.Type {
	case html.TextNode:
		writeText(b, n.Data)
		return
	case html.CommentNode:
		return
	case html.ElementNode:
	default:
		return
	}
	if n.Data == "style" && !inSVG {
		b.WriteString("<style>")
		b.WriteString(safeCSS(rawText(n)))
		b.WriteString("</style>")
		return
	}
	if n.Data == "script" || n.Data == "iframe" || n.Data == "object" || n.Data == "embed" || n.Data == "link" || n.Data == "meta" || n.Data == "base" || n.Data == "template" {
		return
	}
	isSVG := inSVG || n.Namespace != "" || n.Data == "svg"
	allowed := renderTags[n.Data]
	if isSVG {
		allowed = svgTags[n.Data]
	}
	if allowed {
		writeStart(b, n.Data, richAttrs(n, isSVG))
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeRich(b, c, isSVG)
	}
	if allowed && (!renderVoidTags[n.Data] || isSVG) {
		writeEnd(b, n.Data)
	}
}

func richAttrs(n *html.Node, svg bool) []html.Attribute {
	var out []html.Attribute
	for _, a := range n.Attr {
		if a.Namespace != "" {
			continue
		}
		key := a.Key
		if svg {
			if svgAttrs[key] || isDataAttr(key) || strings.HasPrefix(key, "aria-") || key == "class" || key == "id" {
				out = append(out, html.Attribute{Key: key, Val: a.Val})
			}
			continue
		}
		keep := key == "class" || key == "id" || key == "title" || key == "lang" || key == "dir" || key == "role" || key == "style" || isDataAttr(key) || strings.HasPrefix(key, "aria-") || renderAttrs[n.Data][key]
		if !keep || strings.HasPrefix(strings.ToLower(key), "on") {
			continue
		}
		if key == "style" {
			if css := safeCSS(a.Val); css != "" {
				out = append(out, html.Attribute{Key: key, Val: css})
			}
			continue
		}
		if (key == "href" || key == "src") && !safeURL(a.Val, n.Data == "img") {
			continue
		}
		if n.Data == "a" && key == "target" {
			continue
		}
		out = append(out, html.Attribute{Key: key, Val: a.Val})
	}
	return out
}

func safeCSS(css string) string {
	lower := strings.ToLower(css)
	for _, bad := range []string{"javascript:", "expression(", "-moz-binding", "behavior:"} {
		if strings.Contains(lower, bad) {
			return ""
		}
	}
	return importCSS.ReplaceAllString(css, "")
}
