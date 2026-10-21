package render

import (
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var rawSource = set("kapi-code", "kapi-diff")

func rawBodies(src []byte) [][2]int {
	var out [][2]int
	pos := 0
	for {
		z := html.NewTokenizer(bytes.NewReader(src[pos:]))
		off, found := pos, false
		for !found {
			tt := z.Next()
			if tt == html.ErrorToken {
				return out
			}
			raw := len(z.Raw())
			if tt == html.StartTagToken {
				if name, _ := z.TagName(); rawSource[string(name)] {
					start := off + raw
					end := closingTag(src, start, string(name))
					out = append(out, [2]int{start, end})
					pos, found = end, true
				}
			}
			off += raw
		}
		if pos >= len(src) {
			return out
		}
	}
}

func closingTag(src []byte, from int, name string) int {
	want := "</" + name
	for i := from; i+len(want) <= len(src); i++ {
		if src[i] != '<' || !asciiEqualFold(src[i:i+len(want)], want) {
			continue
		}
		if j := i + len(want); j == len(src) || bytes.IndexByte([]byte(" \t\n\r\f/>"), src[j]) >= 0 {
			return i
		}
	}
	return len(src)
}

func asciiEqualFold(b []byte, s string) bool {
	for i := range b {
		c := b[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != s[i] {
			return false
		}
	}
	return true
}

func markRawBodies(src []byte) ([]byte, map[string]string) {
	bodies := rawBodies(src)
	if len(bodies) == 0 {
		return src, nil
	}
	prefix := "kapi-raw-body-"
	for bytes.Contains(src, []byte(prefix)) {
		prefix += "x"
	}
	table := make(map[string]string, len(bodies))
	var b bytes.Buffer
	last := 0
	for i, r := range bodies {
		key := prefix + strconv.Itoa(i)
		table[key] = string(src[r[0]:r[1]])
		b.Write(src[last:r[0]])
		b.WriteString(key)
		last = r[1]
	}
	b.Write(src[last:])
	return b.Bytes(), table
}

func restoreRawBodies(n *html.Node, table map[string]string) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if rawSource[c.Data] && c.Namespace == "" {
			if body, ok := table[rawText(c)]; ok {
				for c.FirstChild != nil {
					c.RemoveChild(c.FirstChild)
				}
				c.AppendChild(&html.Node{Type: html.TextNode, Data: body})
			}
			continue
		}
		restoreRawBodies(c, table)
	}
}

func MaskRawBodies(src []byte) []byte {
	out := bytes.Clone(src)
	for _, r := range rawBodies(src) {
		for i := r[0]; i < r[1]; i++ {
			if out[i] == '<' {
				out[i] = 'x'
			}
		}
	}
	return out
}

func Unwraps(tag string) bool {
	_, allowed := allowedTags[tag]
	return !allowed && !dropped[tag] && !strings.HasPrefix(tag, "kapi-") && tag != "svg" && tag != "math"
}

func CountBlocks(src []byte) (int, error) {
	root, err := load(src)
	if err != nil {
		return 0, err
	}
	n := 0
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Namespace == "" && !dropped[c.Data] {
			n++
		}
	}
	return n, nil
}

func Unlabeled(src []byte) (int, error) {
	root, err := load(src)
	if err != nil {
		return 0, err
	}
	n, seen := 0, map[string]bool{}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Namespace != "" || dropped[c.Data] {
			continue
		}
		id := attrVal(c, "data-kapi-id")
		if id == "" || seen[id] {
			n++
		}
		seen[id] = true
	}
	return n, nil
}
