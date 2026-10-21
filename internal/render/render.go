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
	root, err := load(src)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	renderChildren(&b, root)
	return b.Bytes(), nil
}

func Block(src []byte, id string) ([]byte, error) {
	root, err := load(src)
	if err != nil {
		return nil, err
	}
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		if found := findBlock(n, id); found != nil {
			var b bytes.Buffer
			renderNode(&b, found)
			return b.Bytes(), nil
		}
	}
	return nil, ErrNotFound
}

func load(src []byte) (*html.Node, error) {
	canonical, err := Canonical(src)
	if err != nil {
		return nil, err
	}
	return parse(canonical)
}

func parse(src []byte) (*html.Node, error) {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	marked, bodies := markRawBodies(src)
	nodes, err := html.ParseFragment(bytes.NewReader(marked), body)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		body.AppendChild(n)
	}
	restoreRawBodies(body, bodies)
	var blocks []*html.Node
	collectBlocks(body, &blocks)
	for _, n := range blocks {
		hoist(n)
	}
	dropDuplicateIDs(body, map[string]bool{})
	return body, nil
}

func dropDuplicateIDs(n *html.Node, seen map[string]bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		kept := c.Attr[:0]
		for _, a := range c.Attr {
			if a.Namespace == "" && a.Key == "data-kapi-id" {
				if seen[a.Val] {
					continue
				}
				seen[a.Val] = true
			}
			kept = append(kept, a)
		}
		c.Attr = kept
		if !rawPresets[c.Data] {
			dropDuplicateIDs(c, seen)
		}
	}
}

var blockPresets = set("kapi-callout", "kapi-code", "kapi-diff", "kapi-table", "kapi-compare", "kapi-plan", "kapi-diagram", "kapi-form")

var flowContainers = set("body", "div", "li", "td", "th", "blockquote", "section", "article", "aside", "header", "footer", "details", "dd", "figure", "ul", "ol")

func collectBlocks(n *html.Node, out *[]*html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Namespace != "" || dropped[c.Data] {
			continue
		}
		if blockPresets[c.Data] {
			*out = append(*out, c)
		}
		if !rawPresets[c.Data] {
			collectBlocks(c, out)
		}
	}
}

func hoist(n *html.Node) {
	for parent := n.Parent; !flowContainers[parent.Data] && !strings.HasPrefix(parent.Data, "kapi-"); parent = n.Parent {
		rest := &html.Node{Type: parent.Type, Data: parent.Data, DataAtom: parent.DataAtom}
		for _, a := range parent.Attr {
			if a.Key != "id" && a.Key != "data-kapi-id" {
				rest.Attr = append(rest.Attr, a)
			}
		}
		for c := n.NextSibling; c != nil; {
			next := c.NextSibling
			parent.RemoveChild(c)
			rest.AppendChild(c)
			c = next
		}
		parent.RemoveChild(n)
		grand := parent.Parent
		grand.InsertBefore(n, parent.NextSibling)
		if hasContent(rest) {
			grand.InsertBefore(rest, n.NextSibling)
			if !hasContent(parent) {
				rest.Attr = parent.Attr
			}
		}
		if !hasContent(parent) {
			grand.RemoveChild(parent)
		}
	}
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
	if isPreset(n) {
		renderPreset(b, n)
		return
	}
	writeProse(b, n, renderNode)
}

func renderChildren(b *bytes.Buffer, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderNode(b, c)
	}
}

func isPreset(n *html.Node) bool {
	return n.Type == html.ElementNode && n.Namespace == "" && strings.HasPrefix(n.Data, "kapi-")
}

func writeProse(b *bytes.Buffer, n *html.Node, child func(*bytes.Buffer, *html.Node)) {
	switch n.Type {
	case html.TextNode:
		writeText(b, n.Data)
		return
	case html.CommentNode:
		b.WriteString("<!--" + n.Data + "-->")
		return
	case html.ElementNode:
	default:
		return
	}
	if n.Namespace != "" || dropped[n.Data] {
		return
	}
	allowed, ok := allowedTags[n.Data]
	if ok {
		writeStart(b, n.Data, proseAttrs(n, allowed))
		if voidTags[n.Data] {
			return
		}
		if n.Data == "pre" && n.FirstChild != nil && n.FirstChild.Type == html.TextNode && strings.HasPrefix(n.FirstChild.Data, "\n") {
			b.WriteByte('\n')
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		child(b, c)
	}
	if ok {
		writeEnd(b, n.Data)
	}
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
	textEscaper = strings.NewReplacer("&", "&amp;", "\u00a0", "&nbsp;", "<", "&lt;", ">", "&gt;", "\x00", "\ufffd")
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
