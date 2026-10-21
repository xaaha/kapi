package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

//go:embed style.css
var baseCSS []byte

var css = sync.OnceValue(func() []byte {
	var b bytes.Buffer
	b.Write(baseCSS)
	b.WriteString("\n")
	tokenCSS(&b, styles.Get("github"))
	return b.Bytes()
})

func CSS() []byte {
	return bytes.Clone(css())
}

func tokenCSS(b *bytes.Buffer, style *chroma.Style) {
	base := style.Get(chroma.Text)
	types := make([]chroma.TokenType, 0, len(chroma.StandardTypes))
	for t, cls := range chroma.StandardTypes {
		if t >= 0 && cls != "" && cls != "w" {
			types = append(types, t)
		}
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	for _, t := range types {
		e := style.Get(t)
		var decl bytes.Buffer
		if e.Colour.IsSet() && e.Colour != base.Colour {
			fmt.Fprintf(&decl, "color:%s;", e.Colour)
		}
		if e.Bold == chroma.Yes {
			decl.WriteString("font-weight:600;")
		}
		if e.Italic == chroma.Yes {
			decl.WriteString("font-style:italic;")
		}
		if e.Underline == chroma.Yes {
			decl.WriteString("text-decoration:underline;")
		}
		if decl.Len() > 0 {
			fmt.Fprintf(b, ".kapi-hl .t-%s{%s}\n", chroma.StandardTypes[t], decl.String())
		}
	}
}
