package render

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// renderHTMLComponent progressively enhances standard HTML whose structure
// already expresses a Kapi component. The source remains ordinary HTML.
func renderHTMLComponent(b *bytes.Buffer, n *html.Node) bool {
	if n.Type != html.ElementNode || n.Namespace != "" {
		return false
	}
	switch n.Data {
	case "details":
		if detailsSummary(n) == nil {
			return false
		}
		writeStart(b, "details", componentAttrs(n, "kapi-details", "details"))
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderNode(b, c)
		}
		writeEnd(b, "details")
		return true
	case "pre":
		return renderHTMLCode(b, n)
	case "dl":
		if !definitionList(n) {
			return false
		}
		writeStart(b, "dl", componentAttrs(n, "kapi-kv", "kv"))
		renderChildren(b, n)
		writeEnd(b, "dl")
		return true
	case "figure":
		if n.Parent != nil && n.Parent.Data == "kapi-gallery" {
			return false
		}
		if !hasDirectChild(n, "img") {
			return false
		}
		writeStart(b, "figure", componentAttrs(n, "kapi-figure", "figure"))
		renderChildren(b, n)
		writeEnd(b, "figure")
		return true
	case "ul":
		if !nativeChecklist(n) {
			return false
		}
		writeStart(b, "ul", componentAttrs(n, "kapi-checklist", "checklist"))
		renderChildren(b, n)
		writeEnd(b, "ul")
		return true
	case "li":
		if n.Parent == nil || n.Parent.Data != "ul" || !nativeChecklist(n.Parent) {
			return false
		}
		box := nativeTaskBox(n)
		if box == nil {
			return false
		}
		writeStart(b, "li", componentAttrs(n, "kapi-task", "task"))
		attrs := proseAttrs(box, allowedTags["input"])
		attrs = setHTMLAttr(attrs, "aria-label", nativeTaskText(box))
		writeStart(b, "input", attrs)
		writeStart(b, "span", bodyAttrs(n, "kapi-task-body"))
		for c := box.NextSibling; c != nil; c = c.NextSibling {
			renderNode(b, c)
		}
		b.WriteString("</span></li>")
		return true
	}
	return false
}

func renderHTMLCode(b *bytes.Buffer, n *html.Node) bool {
	code := n.FirstChild
	for code != nil && code.Type == html.TextNode && strings.TrimSpace(code.Data) == "" {
		code = code.NextSibling
	}
	if code == nil {
		return false
	}
	src := rawText(n)
	lang := ""
	if code.Type == html.ElementNode && code.Data == "code" {
		for c := code.NextSibling; c != nil; c = c.NextSibling {
			if c.Type != html.TextNode || strings.TrimSpace(c.Data) != "" {
				return false
			}
		}
		src = rawText(code)
		for _, class := range strings.Fields(attrVal(code, "class")) {
			if strings.HasPrefix(class, "language-") {
				lang = strings.TrimPrefix(class, "language-")
				break
			}
		}
	} else if code.NextSibling != nil {
		return false
	}
	fake := &html.Node{Type: html.ElementNode, Data: "kapi-code"}
	fake.Attr = append(fake.Attr, n.Attr...)
	if lang != "" {
		fake.Attr = append(fake.Attr, html.Attribute{Key: "lang", Val: lang})
	}
	fake.AppendChild(&html.Node{Type: html.TextNode, Data: src})
	return renderCode(b, fake) == nil
}

func componentAttrs(n *html.Node, class, component string) []html.Attribute {
	attrs := proseAttrs(n, allowedTags[n.Data])
	attrs = setHTMLAttr(attrs, "class", strings.TrimSpace(attrValFrom(attrs, "class")+" "+class))
	return setHTMLAttr(attrs, "data-kapi-component", component)
}

func setHTMLAttr(attrs []html.Attribute, key, value string) []html.Attribute {
	for i := range attrs {
		if attrs[i].Namespace == "" && attrs[i].Key == key {
			attrs[i].Val = value
			return attrs
		}
	}
	return append(attrs, html.Attribute{Key: key, Val: value})
}

func attrValFrom(attrs []html.Attribute, key string) string {
	for _, a := range attrs {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func detailsSummary(n *html.Node) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if c.Type == html.ElementNode && c.Data == "summary" {
			return c
		}
		return nil
	}
	return nil
}

func definitionList(n *html.Node) bool {
	want := "dt"
	count := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if c.Type != html.ElementNode || c.Data != want {
			return false
		}
		count++
		if want == "dt" {
			want = "dd"
		} else {
			want = "dt"
		}
	}
	return count > 0 && want == "dt"
}

func nativeChecklist(n *html.Node) bool {
	count := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if c.Type != html.ElementNode || c.Data != "li" || nativeTaskBox(c) == nil {
			return false
		}
		count++
	}
	return count > 0
}

func nativeTaskBox(n *html.Node) *html.Node {
	if n.Type != html.ElementNode || n.Data != "li" {
		return nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if c.Type == html.ElementNode && c.Data == "input" && strings.EqualFold(attrVal(c, "type"), "checkbox") {
			return c
		}
		return nil
	}
	return nil
}

func nativeTaskText(box *html.Node) string {
	var sb strings.Builder
	for c := box.NextSibling; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
		}
		if c.Type == html.ElementNode {
			sb.WriteString(nodeText(c))
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

func hasDirectChild(n *html.Node, tag string) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			return true
		}
	}
	return false
}
