package render

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// renderTable defines the browser table controller's DOM contract: a preset
// wrapper with exactly one direct table child. Runtime layout attributes belong
// to the controller and are never emitted by the renderer.
func renderTable(b *bytes.Buffer, n *html.Node) error {
	kids := elementChildren(n)
	if len(kids) != 1 || kids[0].Data != "table" {
		return fmt.Errorf("body must be exactly one <table>")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) != "" {
			return fmt.Errorf("body must be exactly one <table>; move the text %q into a <p> outside it", strings.TrimSpace(c.Data))
		}
	}
	writeStart(b, "div", outerAttrs(n, "kapi-table"))
	renderNode(b, kids[0])
	writeEnd(b, "div")
	return nil
}
