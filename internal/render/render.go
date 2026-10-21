package render

import (
	"bytes"
	"errors"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type Preset struct{ Name, Summary, Example string }

var ErrNotFound = errors.New("render: no element with that data-kapi-id")

func Render(src []byte) ([]byte, error) {
	nodes, err := parse(src)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for _, n := range nodes {
		renderNode(&b, n)
	}
	return b.Bytes(), nil
}

func Block(src []byte, id string) ([]byte, error) {
	nodes, err := parse(src)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if found := findBlock(n, id); found != nil {
			var b bytes.Buffer
			renderNode(&b, found)
			return b.Bytes(), nil
		}
	}
	return nil, ErrNotFound
}

func parse(src []byte) ([]*html.Node, error) {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	return html.ParseFragment(bytes.NewReader(src), body)
}

func findBlock(n *html.Node, id string) *html.Node {
	if n.Type != html.ElementNode || n.Namespace != "" || dropped[n.Data] {
		return nil
	}
	if v, ok := attr(n, "data-kapi-id"); ok && v == id {
		return n
	}
	if rawPresets[n.Data] || (strings.HasPrefix(n.Data, "kapi-") && !presetRenders(n)) {
		return nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findBlock(c, id); found != nil {
			return found
		}
	}
	return nil
}

func renderNode(b *bytes.Buffer, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		writeText(b, n.Data)
	case html.ElementNode:
		renderElement(b, n)
	case html.CommentNode:
		b.WriteString("<!--" + n.Data + "-->")
	}
}

func renderChildren(b *bytes.Buffer, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderNode(b, c)
	}
}

func renderElement(b *bytes.Buffer, n *html.Node) {
	if n.Namespace != "" || dropped[n.Data] {
		return
	}
	if strings.HasPrefix(n.Data, "kapi-") {
		renderPreset(b, n)
		return
	}
	allowed, ok := allowedTags[n.Data]
	if !ok {
		renderChildren(b, n)
		return
	}
	writeStart(b, n.Data, proseAttrs(n, allowed))
	if voidTags[n.Data] {
		return
	}
	if n.Data == "pre" && n.FirstChild != nil && n.FirstChild.Type == html.TextNode && strings.HasPrefix(n.FirstChild.Data, "\n") {
		b.WriteByte('\n')
	}
	renderChildren(b, n)
	writeEnd(b, n.Data)
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func attrVal(n *html.Node, key string) string {
	v, _ := attr(n, key)
	return v
}

func writeStart(b *bytes.Buffer, tag string, attrs []html.Attribute) {
	b.WriteByte('<')
	b.WriteString(tag)
	for _, a := range attrs {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteString(`="`)
		b.WriteString(attrEscaper.Replace(a.Val))
		b.WriteByte('"')
	}
	b.WriteByte('>')
}

func writeEnd(b *bytes.Buffer, tag string) {
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteByte('>')
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "\u00a0", "&nbsp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "\u00a0", "&nbsp;", `"`, "&quot;")
)

func escText(s string) string { return textEscaper.Replace(s) }

func writeText(b *bytes.Buffer, s string) {
	b.WriteString(escText(s))
}

func kv(pairs ...string) []html.Attribute {
	out := make([]html.Attribute, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, html.Attribute{Key: pairs[i], Val: pairs[i+1]})
	}
	return out
}

func outerAttrs(n *html.Node, class string, extra ...string) []html.Attribute {
	out := kv("class", class, "data-kapi-preset", strings.TrimPrefix(n.Data, "kapi-"))
	out = append(out, kv(extra...)...)
	for _, a := range n.Attr {
		if a.Namespace == "" && isDataAttr(a.Key) && a.Key != "data-kapi-preset" {
			out = append(out, html.Attribute{Key: a.Key, Val: a.Val})
		}
	}
	return out
}

func rawText(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			b.WriteString(c.Data)
		case html.ElementNode:
			var eb bytes.Buffer
			if err := html.Render(&eb, c); err == nil {
				b.Write(eb.Bytes())
			}
		case html.CommentNode:
			b.WriteString("<!--" + c.Data + "-->")
		}
	}
	return b.String()
}

func elementChildren(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

func hasContent(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode || (c.Type == html.TextNode && strings.TrimSpace(c.Data) != "") {
			return true
		}
	}
	return false
}
