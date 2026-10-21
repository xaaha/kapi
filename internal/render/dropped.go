package render

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

type Report struct {
	Elements   map[string]int
	Attributes int
}

func (r Report) Empty() bool {
	return len(r.Elements) == 0 && r.Attributes == 0
}

func (r Report) String() string {
	names := make([]string, 0, len(r.Elements))
	for name := range r.Elements {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if r.Elements[names[i]] != r.Elements[names[j]] {
			return r.Elements[names[i]] > r.Elements[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%d %s", r.Elements[name], name))
	}
	switch {
	case r.Attributes == 1:
		parts = append(parts, "1 attribute")
	case r.Attributes > 1:
		parts = append(parts, fmt.Sprintf("%d attributes", r.Attributes))
	}
	return strings.Join(parts, ", ")
}

func Dropped(src []byte) (Report, error) {
	r := Report{Elements: map[string]int{}}
	z := html.NewTokenizer(bytes.NewReader(src))
	for tt := z.Next(); tt != html.ErrorToken; tt = z.Next() {
		switch tt {
		case html.DoctypeToken:
			r.Elements["doctype"]++
		case html.StartTagToken, html.SelfClosingTagToken:
			if tok := z.Token(); tok.Data == "html" || tok.Data == "head" || tok.Data == "body" {
				r.Elements[tok.Data]++
				r.Attributes += len(tok.Attr)
			}
		}
	}
	root, err := parse(src)
	if err != nil {
		return Report{}, err
	}
	r.walk(root)
	return r, nil
}

func (r *Report) walk(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch allowed, ok := allowedTags[c.Data]; {
		case c.Namespace != "" || dropped[c.Data]:
			r.Elements[c.Data]++
			continue
		case isPreset(c):
			r.Attributes += len(c.Attr) - len(keptPresetAttrs(c))
			if rawPresets[c.Data] {
				continue
			}
		case ok:
			r.Attributes += len(c.Attr) - len(proseAttrs(c, allowed))
		default:
			r.Elements[c.Data]++
		}
		r.walk(c)
	}
}
