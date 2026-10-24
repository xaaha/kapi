package render

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type TextPart int

const (
	NoText TextPart = iota
	BodyText
	CellText
	RawText
	LeadText
)

var textParts = map[string]TextPart{
	"kapi-callout":   BodyText,
	"kapi-code":      RawText,
	"kapi-diff":      RawText,
	"kapi-table":     CellText,
	"kapi-compare":   NoText,
	"kapi-option":    BodyText,
	"kapi-plan":      NoText,
	"kapi-step":      BodyText,
	"kapi-risk":      BodyText,
	"kapi-question":  BodyText,
	"kapi-diagram":   RawText,
	"kapi-form":      NoText,
	"kapi-choice":    LeadText,
	"kapi-opt":       BodyText,
	"kapi-text":      BodyText,
	"kapi-badge":     BodyText,
	"kapi-kv":        NoText,
	"kapi-item":      BodyText,
	"kapi-details":   BodyText,
	"kapi-checklist": NoText,
	"kapi-task":      BodyText,
}

var textAttrs = map[string]string{
	"kapi-item":    "key",
	"kapi-option":  "title",
	"kapi-details": "summary",
}

func TextAttr(tag string) string { return textAttrs[tag] }

func TextOf(tag string) TextPart {
	if !strings.HasPrefix(tag, "kapi-") {
		return BodyText
	}
	return textParts[tag]
}

const (
	textAttr = "data-kapi-text"
	rawAttr  = "data-kapi-raw"
	cellAttr = "data-kapi-cell"
	attrAttr = "data-kapi-attr"
	leadAttr = "data-kapi-lead"
)

var textMarks = set(textAttr, rawAttr, cellAttr, attrAttr, leadAttr)

func markText(root *html.Node) {
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Namespace != "" {
				continue
			}
			unmark(c)
			if !rawPresets[c.Data] {
				walk(c)
			}
			id := attrVal(c, "data-kapi-id")
			switch {
			case id == "":
			case !isPreset(c) && n == root:
				setAttrs(c, textAttr, id)
			case TextOf(c.Data) == RawText:
				setAttrs(c, textAttr, id, rawAttr, "")
			case (TextOf(c.Data) == BodyText || TextOf(c.Data) == LeadText || TextAttr(c.Data) != "") && isPreset(c):
				setAttrs(c, textAttr, id)
			case c.Data == "kapi-table":
				markCells(c, id)
			}
		}
	}
	walk(root)
}

func markCells(n *html.Node, id string) {
	kids := elementChildren(n)
	if len(kids) != 1 || kids[0].Data != "table" {
		return
	}
	row := 0
	var walk func(p *html.Node)
	walk = func(p *html.Node) {
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "tr":
				col := 0
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
						setAttrs(cell, textAttr, id, cellAttr, strconv.Itoa(row)+","+strconv.Itoa(col))
						col++
					}
				}
				row++
			case "thead", "tbody", "tfoot":
				walk(c)
			}
		}
	}
	walk(kids[0])
}

func unmark(n *html.Node) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Namespace != "" || !textMarks[a.Key] {
			kept = append(kept, a)
		}
	}
	n.Attr = kept
}

func setAttrs(n *html.Node, pairs ...string) {
	n.Attr = append(n.Attr, kv(pairs...)...)
}

func attrHost(n *html.Node, class string) []html.Attribute {
	out := kv("class", class)
	if class == "" {
		out = nil
	}
	if id, ok := attr(n, textAttr); ok && TextAttr(n.Data) != "" {
		out = append(out, kv(textAttr, id, attrAttr, TextAttr(n.Data))...)
	}
	return out
}

func leadHost(n *html.Node, contiguous bool) []html.Attribute {
	if id, ok := attr(n, textAttr); ok && contiguous {
		return kv(textAttr, id, leadAttr, "")
	}
	return nil
}

func bodyAttrs(n *html.Node, class string) []html.Attribute {
	var out []html.Attribute
	if class != "" {
		out = kv("class", class)
	}
	if id, ok := attr(n, textAttr); ok {
		out = append(out, kv(textAttr, id)...)
	}
	return out
}
