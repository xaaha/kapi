package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"sync"

	"github.com/alecthomas/chroma/v2"
)

//go:embed theme.css
var themeCSS []byte

//go:embed paper.css
var paperCSS []byte

//go:embed style.css
var baseCSS []byte

var css = sync.OnceValue(func() []byte {
	var b bytes.Buffer
	b.Write(themeCSS)
	b.WriteString("\n")
	b.Write(paperCSS)
	b.WriteString("\n")
	b.Write(baseCSS)
	b.WriteString("\n")
	tokenCSS(&b)
	return b.Bytes()
})

func CSS() []byte {
	return bytes.Clone(css())
}

func tokenCSS(b *bytes.Buffer) {
	types := make([]chroma.TokenType, 0, len(chroma.StandardTypes))
	for t, cls := range chroma.StandardTypes {
		if t >= 0 && cls != "" && cls != "w" {
			types = append(types, t)
		}
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	for _, t := range types {
		if decl := tokenDecl(t); decl != "" {
			fmt.Fprintf(b, ".kapi-hl .t-%s{%s}\n", chroma.StandardTypes[t], decl)
		}
	}
}

var syntaxColour = map[string]string{
	"keyword":  "purple-text",
	"type":     "blue-text",
	"function": "blue-text",
	"string":   "green-text",
	"constant": "yellow-text",
	"tag":      "red-text",
	"variable": "red-text",
	"comment":  "faint",
	"operator": "muted",
	"inserted": "green-text",
	"deleted":  "red-text",
	"heading":  "purple-text",
	"output":   "muted",
}

func tokenDecl(t chroma.TokenType) string {
	code := func(role string) string { return "color:var(--kapi-" + syntaxColour[role] + ");" }
	switch {
	case t == chroma.KeywordType:
		return code("type")
	case t == chroma.KeywordConstant:
		return code("constant")
	case t.InCategory(chroma.Keyword), t == chroma.CommentPreproc, t == chroma.CommentPreprocFile:
		return code("keyword")
	case t.InCategory(chroma.Comment):
		return code("comment") + "font-style:italic;"
	case t == chroma.NameFunction, t == chroma.NameFunctionMagic, t == chroma.NameDecorator:
		return code("function")
	case t == chroma.NameClass, t == chroma.NameBuiltin, t == chroma.NameBuiltinPseudo, t == chroma.NameNamespace, t == chroma.NameException:
		return code("type")
	case t == chroma.NameTag:
		return code("tag")
	case t == chroma.NameAttribute, t == chroma.NameConstant, t == chroma.NameEntity, t == chroma.NameLabel:
		return code("constant")
	case t.InSubCategory(chroma.NameVariable):
		return code("variable")
	case t.InSubCategory(chroma.LiteralString):
		return code("string")
	case t == chroma.Literal, t == chroma.LiteralDate, t.InSubCategory(chroma.LiteralNumber):
		return code("constant")
	case t.InCategory(chroma.Operator):
		return code("operator")
	case t == chroma.GenericInserted:
		return code("inserted")
	case t == chroma.GenericDeleted, t == chroma.GenericError, t == chroma.Error:
		return code("deleted")
	case t == chroma.GenericHeading, t == chroma.GenericSubheading:
		return code("heading") + "font-weight:600;"
	case t == chroma.GenericPrompt, t == chroma.GenericOutput, t == chroma.GenericTraceback:
		return code("output")
	case t == chroma.GenericEmph:
		return "font-style:italic;"
	case t == chroma.GenericStrong:
		return "font-weight:600;"
	case t == chroma.GenericUnderline:
		return "text-decoration:underline;"
	}
	return ""
}
