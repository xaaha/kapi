package render

import (
	"strings"

	"golang.org/x/net/html"
)

var dropped = set(
	"script", "style", "iframe", "frame", "frameset", "object", "embed", "applet",
	"noscript", "noembed", "noframes", "template", "base", "link",
	"meta", "title", "head", "textarea", "select", "option", "optgroup", "button",
	"input", "xmp", "plaintext", "param", "source", "track", "audio", "video",
	"canvas", "portal", "dialog", "slot", "datalist", "output", "keygen", "picture",
)

var voidTags = set("br", "hr", "img", "wbr", "col")

var rawPresets = set("kapi-code", "kapi-diff", "kapi-diagram")

var globalAttrs = set("id", "class", "title", "lang", "dir", "role")

var allowedTags = map[string]map[string]bool{
	"a": set("href"), "abbr": nil, "article": nil, "aside": nil, "b": nil,
	"blockquote": set("cite"), "br": nil, "caption": nil, "cite": nil, "code": nil,
	"col": set("span"), "colgroup": set("span"), "dd": nil, "del": set("cite", "datetime"),
	"details": set("open"), "dfn": nil, "div": nil, "dl": nil, "dt": nil, "em": nil,
	"figcaption": nil, "figure": nil, "footer": nil, "h1": nil, "h2": nil, "h3": nil,
	"h4": nil, "h5": nil, "h6": nil, "header": nil, "hr": nil, "i": nil,
	"img": set("src", "alt", "width", "height"), "ins": set("cite", "datetime"),
	"kbd": nil, "li": set("value"), "mark": nil, "ol": set("start", "reversed", "type"),
	"p": nil, "pre": nil, "q": set("cite"), "s": nil, "samp": nil, "section": nil,
	"small": nil, "span": nil, "strong": nil, "sub": nil, "summary": nil, "sup": nil,
	"table": nil, "tbody": nil, "td": set("colspan", "rowspan", "headers"), "tfoot": nil,
	"th": set("colspan", "rowspan", "headers", "scope", "abbr"), "thead": nil,
	"time": set("datetime"), "tr": nil, "u": nil, "ul": nil, "var": nil, "wbr": nil,
}

var urlAttrs = set("href", "src", "cite")

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func proseAttrs(n *html.Node, allowed map[string]bool) []html.Attribute {
	var out []html.Attribute
	for _, a := range n.Attr {
		if a.Namespace != "" {
			continue
		}
		key := a.Key
		switch {
		case key == "data-kapi-preset":
			continue
		case isDataAttr(key), strings.HasPrefix(key, "aria-"):
		case key == "id":
			if !safeID(a.Val) {
				continue
			}
		case globalAttrs[key], allowed[key]:
		default:
			continue
		}
		if urlAttrs[key] && !safeURL(a.Val, n.Data == "img" && key == "src") {
			continue
		}
		out = append(out, html.Attribute{Key: key, Val: a.Val})
	}
	return out
}

func isDataAttr(key string) bool {
	if !strings.HasPrefix(key, "data-") || len(key) == len("data-") {
		return false
	}
	for _, r := range key[len("data-"):] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func safeID(id string) bool {
	if id == "" || strings.HasPrefix(strings.ToLower(id), "kapi") {
		return false
	}
	for _, r := range id {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

func safeURL(raw string, image bool) bool {
	u := strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	colon := strings.IndexByte(u, ':')
	if colon < 0 || strings.ContainsAny(u[:colon], "/?#") {
		return true
	}
	switch strings.ToLower(u[:colon]) {
	case "http", "https", "mailto", "tel":
		return true
	case "data":
		if !image {
			return false
		}
		lower := strings.ToLower(u)
		for _, t := range []string{"data:image/png", "data:image/jpeg", "data:image/gif", "data:image/webp"} {
			if strings.HasPrefix(lower, t+";") || strings.HasPrefix(lower, t+",") {
				return true
			}
		}
	}
	return false
}
