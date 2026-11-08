package page

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const summaryRunes = 240

// Preview is the text of the page's first h1 and of its first non-empty paragraph, cut to a few lines.
func Preview(src []byte) (title, summary string) {
	z := html.NewTokenizer(bytes.NewReader(src))
	var in atom.Atom
	depth := 0
	var b strings.Builder
	for title == "" || summary == "" {
		switch z.Next() {
		case html.ErrorToken:
			return title, summary
		case html.StartTagToken:
			name, _ := z.TagName()
			a := atom.Lookup(name)
			if in == 0 && ((a == atom.H1 && title == "") || (a == atom.P && summary == "")) {
				in, depth = a, 0
				b.Reset()
			}
			if a == in {
				depth++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if a := atom.Lookup(name); a == in && in != 0 {
				if depth--; depth > 0 {
					continue
				}
				text := strings.Join(strings.Fields(b.String()), " ")
				if in == atom.H1 {
					title = text
				} else {
					summary = clip(text)
				}
				in = 0
			}
		case html.TextToken:
			if in != 0 {
				b.Write(z.Text())
			}
		}
	}
	return title, summary
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= summaryRunes {
		return s
	}
	r := []rune(s)[:summaryRunes]
	if i := strings.LastIndexByte(string(r), ' '); i > summaryRunes/2 {
		return string(r)[:i] + "…"
	}
	return string(r) + "…"
}
