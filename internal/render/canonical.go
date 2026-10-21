package render

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

var presetSourceAttrs = map[string]map[string]bool{
	"kapi-callout": set("kind"),
	"kapi-code":    set("lang", "file", "lines"),
	"kapi-diff":    set("lang", "file"),
	"kapi-table":   set("sort"),
	"kapi-diagram": set("type"),
	"kapi-form":    set("name"),
	"kapi-choice":  set("name", "multiple"),
	"kapi-opt":     set("value"),
	"kapi-text":    set("name"),
	"kapi-option":  set("title"),
}

var rawEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;")

func Canonical(src []byte) ([]byte, error) {
	out, err := canonicalOnce(src)
	for i := 0; err == nil && i < 8; i++ {
		var next []byte
		if next, err = canonicalOnce(out); err == nil && bytes.Equal(next, out) {
			return out, nil
		}
		out = next
	}
	return out, err
}

func canonicalOnce(src []byte) ([]byte, error) {
	root, err := parse(src)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		canonicalNode(&b, c)
	}
	return b.Bytes(), nil
}

func canonicalNode(b *bytes.Buffer, n *html.Node) {
	if !isPreset(n) {
		writeProse(b, n, canonicalNode)
		return
	}
	writeStart(b, n.Data, keptPresetAttrs(n))
	if rawPresets[n.Data] {
		if rawSource[n.Data] {
			b.WriteString(rawText(n))
		} else {
			b.WriteString(rawEscaper.Replace(rawText(n)))
		}
	} else {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			canonicalNode(b, c)
		}
	}
	writeEnd(b, n.Data)
}

func keptPresetAttrs(n *html.Node) []html.Attribute {
	keep := presetSourceAttrs[n.Data]
	var attrs []html.Attribute
	for _, a := range n.Attr {
		if a.Namespace == "" && (keep[a.Key] || (isDataAttr(a.Key) && a.Key != "data-kapi-preset")) {
			attrs = append(attrs, a)
		}
	}
	return attrs
}
