package page

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"

	"github.com/xaaha/kapi/internal/render"
)

const idAttr = "data-kapi-id"

type element struct {
	name        string
	id          string
	attrs       []html.Attribute
	selfClosing bool
	start       int
	tagEnd      int
	closeStart  int
	end         int
	parent      int
}

func (e *element) attr(key string) (string, bool) {
	for _, a := range e.attrs {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func (e *element) contains(o *element) bool {
	return o.start >= e.start && o.start < e.end
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true,
}

var closesParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "div": true, "dl": true,
	"fieldset": true, "footer": true, "form": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "header": true, "hr": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "ul": true,
}

var closesSelf = map[string]bool{"li": true, "dt": true, "dd": true, "tr": true, "td": true, "th": true, "option": true}

func parse(src []byte) []element {
	var els []element
	var open []int
	closeAt := func(i, closeStart, end int) {
		els[i].closeStart, els[i].end = closeStart, end
	}
	z := html.NewTokenizer(bytes.NewReader(render.MaskRawBodies(src)))
	off := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		start := off
		off += len(z.Raw())
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if n := len(open); n > 0 {
				top := els[open[n-1]].name
				if (top == "p" && closesParagraph[tok.Data]) || (top == tok.Data && closesSelf[top]) {
					closeAt(open[n-1], start, start)
					open = open[:n-1]
				}
			}
			e := element{name: tok.Data, attrs: tok.Attr, selfClosing: tt == html.SelfClosingTagToken, start: start, tagEnd: off, parent: -1}
			if n := len(open); n > 0 {
				e.parent = open[n-1]
			}
			e.id, _ = e.attr(idAttr)
			els = append(els, e)
			if voidElements[tok.Data] {
				closeAt(len(els)-1, off, off)
				continue
			}
			open = append(open, len(els)-1)
		case html.EndTagToken:
			name, _ := z.TagName()
			for k := len(open) - 1; k >= 0; k-- {
				if els[open[k]].name != string(name) {
					continue
				}
				for _, i := range open[k+1:] {
					closeAt(i, start, start)
				}
				closeAt(open[k], start, off)
				open = open[:k]
				break
			}
		}
	}
	for _, i := range open {
		closeAt(i, len(src), len(src))
	}
	return els
}

func find(els []element, id string) (*element, error) {
	for i := range els {
		if els[i].id == id {
			return &els[i], nil
		}
	}
	return nil, fmt.Errorf("%w: #%s", ErrNotFound, id)
}

// Outer returns the source of the element with data-kapi-id id, from its start tag to its end tag.
func Outer(src []byte, id string) ([]byte, error) {
	e, err := find(parse(src), id)
	if err != nil {
		return nil, err
	}
	return src[e.start:e.end], nil
}

// FormID returns the data-kapi-id of the kapi-form named name.
func FormID(src []byte, name string) (string, error) {
	for _, e := range parse(src) {
		if n, _ := e.attr("name"); e.name == "kapi-form" && n == name {
			return e.id, nil
		}
	}
	return "", fmt.Errorf("%w: no kapi-form named %q", ErrNotFound, name)
}

func newID(taken map[string]bool) string {
	for {
		b := make([]byte, 5)
		if _, err := io.ReadFull(rand.Reader, b); err != nil {
			panic(err)
		}
		id := strings.ToLower(base32.StdEncoding.EncodeToString(b))
		if !taken[id] {
			taken[id] = true
			return id
		}
	}
}

func transparent(name string) bool {
	return name == "html" || name == "head" || name == "body" || render.Unwraps(name)
}

func units(els []element) []int {
	var out []int
	for i := range els {
		if transparent(els[i].name) {
			continue
		}
		top := true
		for p := els[i].parent; p >= 0 && top; p = els[p].parent {
			top = transparent(els[p].name)
		}
		if top {
			out = append(out, i)
		}
	}
	return out
}

func assignIDs(src []byte, els []element, need map[int]bool) []byte {
	taken := map[string]bool{}
	first := map[string]int{}
	for i, e := range els {
		if _, ok := first[e.id]; e.id != "" && !ok {
			taken[e.id] = true
			first[e.id] = i
		}
	}
	for i := len(els) - 1; i >= 0; i-- {
		e := &els[i]
		if (need[i] || strings.HasPrefix(e.name, "kapi-")) && (e.id == "" || first[e.id] != i) {
			src = setAttr(src, e, idAttr, newID(taken))
		}
	}
	return src
}

func setAttr(src []byte, e *element, key, val string) []byte {
	quoted := `"` + html.EscapeString(val) + `"`
	if from, to, ok := attrValue(src[e.start:e.tagEnd], key); ok {
		if from == to {
			quoted = "=" + quoted
		}
		return splice(src, e.start+from, e.start+to, quoted)
	}
	at := e.start + 1
	for at < e.tagEnd && !strings.ContainsRune(" \t\n\r\f/>", rune(src[at])) {
		at++
	}
	return splice(src, at, at, " "+key+"="+quoted)
}

func attrValue(tag []byte, key string) (int, int, bool) {
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
	i := 1
	for i < len(tag) && !isSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
		i++
	}
	for i < len(tag) {
		for i < len(tag) && (isSpace(tag[i]) || tag[i] == '/') {
			i++
		}
		if i >= len(tag) || tag[i] == '>' {
			return 0, 0, false
		}
		nameStart := i
		for i < len(tag) && !isSpace(tag[i]) && tag[i] != '=' && tag[i] != '>' && tag[i] != '/' {
			i++
		}
		name := strings.ToLower(string(tag[nameStart:i]))
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] != '=' {
			if name == key {
				return i, i, true
			}
			continue
		}
		i++
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		valStart := i
		if i < len(tag) && (tag[i] == '"' || tag[i] == '\'') {
			q := tag[i]
			i++
			for i < len(tag) && tag[i] != q {
				i++
			}
			if i < len(tag) {
				i++
			}
		} else {
			for i < len(tag) && !isSpace(tag[i]) && tag[i] != '>' {
				i++
			}
		}
		if name == key {
			return valStart, i, true
		}
	}
	return 0, 0, false
}

func splice(src []byte, from, to int, with string) []byte {
	out := make([]byte, 0, len(src)-(to-from)+len(with))
	out = append(out, src[:from]...)
	out = append(out, with...)
	return append(out, src[to:]...)
}

func lineStart(src []byte, at int) (int, bool) {
	ls := bytes.LastIndexByte(src[:at], '\n') + 1
	return ls, len(bytes.TrimLeft(src[ls:at], " \t")) == 0
}

func lineEnd(src []byte, at int) (int, bool) {
	nl := bytes.IndexByte(src[at:], '\n')
	if nl < 0 {
		return len(src), len(bytes.TrimSpace(src[at:])) == 0
	}
	return at + nl + 1, len(bytes.TrimSpace(src[at:at+nl])) == 0
}

func cutRange(src []byte, e *element) (int, int) {
	ls, aloneBefore := lineStart(src, e.start)
	le, aloneAfter := lineEnd(src, e.end)
	if aloneBefore && aloneAfter {
		return ls, le
	}
	return e.start, e.end
}

func cut(src []byte, e *element) []byte {
	from, to := cutRange(src, e)
	return splice(src, from, to, "")
}

func fragment(s string) ([]byte, *element, error) {
	src := []byte(strings.TrimSpace(s))
	els := parse(src)
	var roots []*element
	for i := range els {
		if els[i].parent < 0 {
			roots = append(roots, &els[i])
		}
	}
	if len(roots) != 1 || roots[0].start != 0 || roots[0].end != len(src) {
		return nil, nil, errors.New("edit html must be exactly one element")
	}
	return src, roots[0], nil
}
