package render

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps(), renderer.WithNodeRenderers(util.Prioritized(rawAsText{}, 100))),
)

type rawAsText struct{}

func (rawAsText) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindRawHTML, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			segs := n.(*ast.RawHTML).Segments
			for i := range segs.Len() {
				seg := segs.At(i)
				_, _ = w.Write(util.EscapeHTML(seg.Value(src)))
			}
		}
		return ast.WalkSkipChildren, nil
	})
	r.Register(ast.KindHTMLBlock, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		block := n.(*ast.HTMLBlock)
		list, inList := n.Parent().Parent().(*ast.List)
		paragraph := !inList || !list.IsTight
		if paragraph {
			_, _ = w.WriteString("<p>")
		}
		lines := block.Lines()
		for i := range lines.Len() {
			line := lines.At(i)
			_, _ = w.Write(util.EscapeHTML(bytes.TrimRight(line.Value(src), "\n")))
			if i < lines.Len()-1 || block.HasClosure() {
				_, _ = w.WriteString("<br>\n")
			}
		}
		if block.HasClosure() {
			_, _ = w.Write(util.EscapeHTML(bytes.TrimRight(block.ClosureLine.Value(src), "\n")))
		}
		if paragraph {
			_, _ = w.WriteString("</p>")
		}
		_ = w.WriteByte('\n')
		return ast.WalkSkipChildren, nil
	})
}

func Message(text string) ([]byte, error) {
	var md bytes.Buffer
	if err := markdown.Convert([]byte(text), &md); err != nil {
		return nil, err
	}
	root, _, err := loadRepaired(md.Bytes())
	if err != nil {
		return nil, err
	}
	var rendered bytes.Buffer
	renderChildren(&rendered, root)
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(&rendered, body)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for _, n := range nodes {
		if n.Type == html.CommentNode {
			continue
		}
		tidyMessage(n)
		if err := html.Render(&out, n); err != nil {
			return nil, err
		}
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

func tidyMessage(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode:
			n.RemoveChild(c)
		case c.Type == html.ElementNode && c.Data == "a" && attrVal(c, "href") == "":
			tidyMessage(c)
			for k := c.FirstChild; k != nil; k = c.FirstChild {
				c.RemoveChild(k)
				n.InsertBefore(k, c)
			}
			n.RemoveChild(c)
		default:
			tidyMessage(c)
		}
		c = next
	}
	if n.Type != html.ElementNode {
		return
	}
	attrs := n.Attr[:0]
	for _, a := range n.Attr {
		if strings.HasPrefix(a.Key, "data-kapi-") || (a.Key == "href" && a.Val == "") {
			continue
		}
		attrs = append(attrs, a)
	}
	n.Attr = attrs
	if n.Data == "a" && attrVal(n, "href") != "" {
		n.Attr = append(n.Attr, html.Attribute{Key: "target", Val: "_blank"}, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
	}
}
