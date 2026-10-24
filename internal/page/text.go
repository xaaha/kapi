package page

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/xaaha/kapi/internal/render"
)

var (
	ErrNoText       = errors.New("it has no text of its own to edit; edit the parts inside it")
	ErrHiddenMarkup = errors.New("the text holds markup the page does not show, so it cannot be edited in place; ask the agent to change it")
)

type Part struct {
	ID    string `json:"id"`
	Row   *int   `json:"row,omitempty"`
	Col   int    `json:"col,omitempty"`
	Inner string `json:"inner"`
}

const keepMark = "kapi-keep:"

func setText(src []byte, op *Op) ([]byte, error) {
	if len(op.Parts) == 0 {
		return nil, errors.New("a text op needs at least one part")
	}
	seen := map[string]bool{}
	for _, p := range op.Parts {
		key := p.ID
		if p.Row != nil {
			key += "/" + strconv.Itoa(*p.Row) + "," + strconv.Itoa(p.Col)
		}
		if seen[key] {
			return nil, fmt.Errorf("the part %s is set twice", key)
		}
		seen[key] = true
		els := parse(src)
		block, err := find(els, op.ID)
		if err != nil {
			return nil, err
		}
		t, err := find(els, p.ID)
		if err != nil {
			return nil, err
		}
		if !block.contains(t) {
			return nil, fmt.Errorf("#%s is not inside #%s", p.ID, op.ID)
		}
		from, to, err := partRange(els, t, p)
		if err != nil {
			return nil, err
		}
		inner := p.Inner
		if p.Row != nil || render.TextOf(t.name) == render.BodyText {
			if inner, err = cleanText(src, els, t.name, from, to, p.Inner); err != nil {
				return nil, err
			}
		}
		with := string(src[t.start:from]) + inner + string(src[to:t.end])
		if src, err = edit(src, els, t, with, p.Row == nil); err != nil {
			return nil, err
		}
	}
	return src, nil
}

func partRange(els []element, t *element, p Part) (int, int, error) {
	if p.Row != nil {
		if render.TextOf(t.name) != render.CellText {
			return 0, 0, fmt.Errorf("a cell part needs a kapi-table, not <%s>", t.name)
		}
		rows := cellsOf(els, t)
		r, c := *p.Row, p.Col
		if r < 0 || r >= len(rows) || c < 0 || c >= len(rows[r]) {
			return 0, 0, fmt.Errorf("the table has no cell at row %d, column %d", r, c)
		}
		return rows[r][c].tagEnd, rows[r][c].closeStart, nil
	}
	if kind := render.TextOf(t.name); (kind != render.BodyText && kind != render.RawText) || voidElements[t.name] || t.selfClosing {
		return 0, 0, fmt.Errorf("%w: #%s is a <%s>", ErrNoText, t.id, t.name)
	}
	return t.tagEnd, t.closeStart, nil
}

func cellsOf(els []element, e *element) [][]*element {
	table := -1
	var rows [][]*element
	for i := range els {
		c := &els[i]
		switch {
		case !e.contains(c):
		case c.name == "table" && table < 0:
			table = i
		case c.name == "tr" && table >= 0 && nearest(els, i, "table") == table:
			rows = append(rows, nil)
		case (c.name == "td" || c.name == "th") && len(rows) > 0 && c.parent >= 0 && els[c.parent].name == "tr" && nearest(els, c.parent, "table") == table:
			rows[len(rows)-1] = append(rows[len(rows)-1], c)
		}
	}
	return rows
}

func cleanText(src []byte, els []element, host string, from, to int, in string) (string, error) {
	kept := map[string]string{}
	for i := range els {
		if e := &els[i]; e.start >= from && e.end <= to && e.id != "" {
			kept[e.id] = string(src[e.start:e.end])
		}
	}
	wrap := host
	if strings.HasPrefix(host, presetPrefix) || host == "td" || host == "th" {
		wrap = "div"
	}
	was, err := masked(string(src[from:to]), wrap)
	if err != nil {
		return "", err
	}
	if canon, err := canonicalIn(was, wrap); err != nil || rerender(canon, wrap) != rerender(was, wrap) {
		return "", ErrHiddenMarkup
	}
	now, err := masked(in, wrap)
	if err != nil {
		return "", err
	}
	out, err := canonicalIn(now, wrap)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		i := strings.Index(out, "<!--"+keepMark)
		if i < 0 {
			b.WriteString(out)
			return b.String(), nil
		}
		j := strings.Index(out[i:], "-->")
		id := out[i+len("<!--"+keepMark) : i+j]
		source, ok := kept[id]
		if !ok {
			return "", fmt.Errorf("#%s is not part of this text", id)
		}
		b.WriteString(out[:i])
		b.WriteString(source)
		delete(kept, id)
		out = out[i+j+len("-->"):]
	}
}

func masked(s, wrap string) (string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: wrap, DataAtom: atom.Lookup([]byte(wrap))})
	if err != nil {
		return "", err
	}
	var walk func(n *html.Node) error
	walk = func(n *html.Node) error {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				c.Data = strings.ReplaceAll(c.Data, "\u200b", "")
			case c.Type != html.ElementNode:
			case isKept(c):
				id := nodeAttr(c, idAttr)
				if id == "" {
					return fmt.Errorf("a <%s> in the text has no %s", c.Data, idAttr)
				}
				keep := &html.Node{Type: html.CommentNode, Data: keepMark + id}
				n.InsertBefore(keep, c)
				n.RemoveChild(c)
				c = keep
			default:
				c.Attr = chromeless(c.Attr)
				if err := walk(c); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var b bytes.Buffer
	for _, n := range nodes {
		holder := &html.Node{Type: html.ElementNode, Data: "div"}
		holder.AppendChild(n)
		if err := walk(holder); err != nil {
			return "", err
		}
		for c := holder.FirstChild; c != nil; c = c.NextSibling {
			if err := html.Render(&b, c); err != nil {
				return "", err
			}
		}
	}
	return b.String(), nil
}

func isKept(n *html.Node) bool {
	return strings.HasPrefix(n.Data, presetPrefix) || nodeAttr(n, "data-kapi-preset") != ""
}

func chromeless(attrs []html.Attribute) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		if a.Key == "contenteditable" || (strings.HasPrefix(a.Key, "data-kapi-") && a.Key != idAttr) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func canonicalIn(s, wrap string) (string, error) {
	out, err := render.Canonical([]byte("<" + wrap + ">" + s + "</" + wrap + ">"))
	if err != nil {
		return "", err
	}
	nodes, err := html.ParseFragment(bytes.NewReader(out), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil || len(nodes) != 1 || nodes[0].Type != html.ElementNode || nodes[0].Data != wrap {
		return "", ErrInnerEscapes
	}
	inner, ok := strings.CutPrefix(string(out), "<"+wrap+">")
	if inner, ok2 := strings.CutSuffix(inner, "</"+wrap+">"); ok && ok2 {
		return inner, nil
	}
	return "", ErrInnerEscapes
}

func rerender(s, wrap string) string {
	out, err := masked(s, wrap)
	if err != nil {
		return ""
	}
	return out
}
