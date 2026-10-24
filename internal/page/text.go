package page

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
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
	Attr  string `json:"attr,omitempty"`
	Lead  bool   `json:"lead,omitempty"`
	Inner string `json:"inner"`
}

func setText(src []byte, op *Op) ([]byte, error) {
	if len(op.Parts) == 0 {
		return nil, errors.New("a text op needs at least one part")
	}
	seen := map[string]bool{}
	for _, p := range op.Parts {
		key := p.ID + "/" + p.Attr + "/" + strconv.FormatBool(p.Lead)
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
		if p.Attr != "" {
			if src, err = setTextAttr(src, t, p); err != nil {
				return nil, err
			}
			continue
		}
		from, to, err := partRange(els, t, p)
		if err != nil {
			return nil, err
		}
		if render.TextOf(t.name) == render.RawText && p.ID != op.ID {
			return nil, fmt.Errorf("#%s is shown as one piece inside #%s; edit it on its own", p.ID, op.ID)
		}
		inner := p.Inner
		if render.TextOf(t.name) != render.RawText {
			if inner, err = cleanText(src, els, t.name, from, to, p.Inner); err != nil {
				return nil, err
			}
		}
		if p.Lead {
			old := string(src[from:to])
			inner = old[:len(old)-len(strings.TrimLeft(old, " \t\r\n"))] + strings.TrimSpace(inner) + old[len(strings.TrimRight(old, " \t\r\n")):]
		}
		with := string(src[t.start:from]) + inner + string(src[to:t.end])
		if src, err = edit(src, els, t, with, p.Row == nil, true); err != nil {
			return nil, err
		}
	}
	return src, nil
}

func textUnchanged(then, now []byte, op *Op) bool {
	for _, p := range op.Parts {
		was, okWas := partSource(then, p)
		is, okIs := partSource(now, p)
		if !okWas || !okIs || was != is {
			return false
		}
	}
	return len(op.Parts) > 0
}

func setTextAttr(src []byte, t *element, p Part) ([]byte, error) {
	if render.TextAttr(t.name) != p.Attr {
		return nil, fmt.Errorf("%w: the %s of a <%s> is not text", ErrNoText, p.Attr, t.name)
	}
	value := strings.Join(strings.Fields(strings.ReplaceAll(p.Inner, "\u200b", "")), " ")
	if value == "" {
		return nil, fmt.Errorf("the %s of #%s cannot be empty", p.Attr, t.id)
	}
	return setAttr(src, t, p.Attr, value), nil
}

func partSource(src []byte, p Part) (string, bool) {
	els := parse(src)
	t, err := find(els, p.ID)
	if err != nil {
		return "", false
	}
	if p.Attr != "" {
		return t.attr(p.Attr)
	}
	from, to, err := partRange(els, t, p)
	if err != nil {
		return "", false
	}
	return string(src[from:to]), true
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
	if p.Lead {
		return leadRange(els, t)
	}
	if kind := render.TextOf(t.name); (kind != render.BodyText && kind != render.RawText) || voidElements[t.name] || t.selfClosing {
		return 0, 0, fmt.Errorf("%w: #%s is a <%s>", ErrNoText, t.id, t.name)
	}
	return t.tagEnd, t.closeStart, nil
}

func leadRange(els []element, t *element) (int, int, error) {
	if render.TextOf(t.name) != render.LeadText {
		return 0, 0, fmt.Errorf("%w: #%s is a <%s>", ErrNoText, t.id, t.name)
	}
	self := -1
	for i := range els {
		if &els[i] == t {
			self = i
		}
	}
	to := -1
	for i := range els {
		if els[i].parent != self {
			continue
		}
		if els[i].name == "kapi-opt" && to < 0 {
			to = els[i].start
		} else if els[i].name != "kapi-opt" && to >= 0 {
			return 0, 0, fmt.Errorf("%w: the question of #%s is split around its options", ErrNoText, t.id)
		}
	}
	if to < 0 {
		to = t.closeStart
	}
	return t.tagEnd, to, nil
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
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	stored := masker{wrap: wrap, mark: "kapi-keep-" + hex.EncodeToString(nonce) + ":"}
	was, err := stored.mask(string(src[from:to]))
	if err != nil {
		return "", err
	}
	if canon, err := canonicalIn(was, wrap); err != nil || stored.rerender(canon) != stored.rerender(was) {
		return "", ErrHiddenMarkup
	}
	typed := stored
	typed.typed, typed.known = true, kept
	now, err := typed.mask(in)
	if err != nil {
		return "", err
	}
	out, err := canonicalIn(now, wrap)
	if err != nil {
		return "", err
	}
	open := "<!--" + stored.mark
	var b strings.Builder
	for {
		i := strings.Index(out, open)
		if i < 0 {
			b.WriteString(out)
			return b.String(), nil
		}
		j := strings.Index(out[i:], "-->")
		id := out[i+len(open) : i+j]
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

type masker struct {
	wrap, mark string
	typed      bool
	known      map[string]string
}

func (m masker) mask(s string) (string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: m.wrap, DataAtom: atom.Lookup([]byte(m.wrap))})
	if err != nil {
		return "", err
	}
	var walk func(n *html.Node) error
	walk = func(n *html.Node) error {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode && m.typed && c.PrevSibling != nil && c.PrevSibling.Data == "code":
				c.Data = strings.TrimPrefix(c.Data, "\u200b")
			case c.Type != html.ElementNode:
			case isKept(c):
				id := nodeAttr(c, idAttr)
				if id == "" {
					return fmt.Errorf("a <%s> in the text has no %s", c.Data, idAttr)
				}
				keep := &html.Node{Type: html.CommentNode, Data: m.mark + id}
				n.InsertBefore(keep, c)
				n.RemoveChild(c)
				c = keep
			default:
				c.Attr = m.chromeless(c.Attr)
				if err := walk(c); err != nil {
					return err
				}
			}
		}
		return nil
	}
	holder := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		holder.AppendChild(n)
	}
	if err := walk(holder); err != nil {
		return "", err
	}
	var b bytes.Buffer
	for c := holder.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func (m masker) chromeless(attrs []html.Attribute) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		_, known := m.known[a.Val]
		if a.Key == "contenteditable" || (strings.HasPrefix(a.Key, "data-kapi-") && a.Key != idAttr) || (m.typed && a.Key == idAttr && !known) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (m masker) rerender(s string) string {
	out, err := m.mask(s)
	if err != nil {
		return ""
	}
	return out
}

func isKept(n *html.Node) bool {
	return strings.HasPrefix(n.Data, presetPrefix) || nodeAttr(n, "data-kapi-preset") != ""
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
