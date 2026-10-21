package render

import (
	"bytes"
	"fmt"

	"golang.org/x/net/html"
)

type presetFunc func(b *bytes.Buffer, n *html.Node) error

var presets map[string]presetFunc

var parents = map[string]string{
	"kapi-option":   "kapi-compare",
	"kapi-step":     "kapi-plan",
	"kapi-risk":     "kapi-plan",
	"kapi-question": "kapi-plan",
	"kapi-choice":   "kapi-form",
	"kapi-text":     "kapi-form",
	"kapi-opt":      "kapi-choice",
}

func init() {
	presets = map[string]presetFunc{
		"kapi-callout":  renderCallout,
		"kapi-code":     renderCode,
		"kapi-diff":     renderDiff,
		"kapi-table":    renderTable,
		"kapi-compare":  renderCompare,
		"kapi-option":   renderOption,
		"kapi-plan":     renderPlan,
		"kapi-step":     planItem("kapi-step", ""),
		"kapi-risk":     planItem("kapi-risk", "Risk"),
		"kapi-question": planItem("kapi-question", "Question"),
		"kapi-diagram":  renderDiagram,
		"kapi-form":     renderForm,
		"kapi-choice":   renderChoice,
		"kapi-opt":      renderOpt,
		"kapi-text":     renderTextField,
	}
}

func renderPreset(b *bytes.Buffer, n *html.Node) {
	var out bytes.Buffer
	if err := tryPreset(&out, n); err != nil {
		errorBox(b, n, err.Error())
		return
	}
	b.Write(out.Bytes())
}

func tryPreset(b *bytes.Buffer, n *html.Node) error {
	fn, ok := presets[n.Data]
	if !ok {
		return fmt.Errorf("unknown preset element; run `kapi preset` to list the ones that exist")
	}
	if want := parents[n.Data]; want != "" && (n.Parent == nil || n.Parent.Data != want) {
		return fmt.Errorf("must be a direct child of <%s>", want)
	}
	return fn(b, n)
}

func presetRenders(n *html.Node) bool {
	var discard bytes.Buffer
	return tryPreset(&discard, n) == nil
}

var phrasing = set("p", "h1", "h2", "h3", "h4", "h5", "h6", "span", "a", "em", "strong", "b", "i", "u", "s",
	"small", "code", "mark", "sub", "sup", "q", "cite", "abbr", "kbd", "samp", "var", "del", "ins", "time",
	"dfn", "summary", "caption", "kapi-opt", "kapi-text", "kapi-choice")

func errorBox(b *bytes.Buffer, n *html.Node, msg string) {
	tag := "div"
	if n.Parent != nil && phrasing[n.Parent.Data] {
		tag = "span"
	}
	writeStart(b, tag, outerAttrs(n, "kapi-error", "role", "alert"))
	b.WriteString(`<span class="kapi-error-tag">`)
	writeText(b, "<"+n.Data+">")
	b.WriteString(`</span> `)
	writeText(b, msg)
	writeEnd(b, tag)
}

func requireAttr(n *html.Node, key string) (string, error) {
	v, ok := attr(n, key)
	if !ok || v == "" {
		return "", fmt.Errorf("needs a %s attribute", key)
	}
	return v, nil
}

var calloutLabels = map[string]string{"note": "Note", "warning": "Warning", "key": "Key point"}

func renderCallout(b *bytes.Buffer, n *html.Node) error {
	kind := attrVal(n, "kind")
	if kind == "" {
		kind = "note"
	}
	label, ok := calloutLabels[kind]
	if !ok {
		return fmt.Errorf("kind must be note, warning or key, not %q", kind)
	}
	writeStart(b, "aside", outerAttrs(n, "kapi-callout kapi-callout-"+kind, "role", "note"))
	b.WriteString(`<div class="kapi-callout-label">` + label + `</div><div class="kapi-callout-body">`)
	renderChildren(b, n)
	b.WriteString("</div></aside>")
	return nil
}

func renderTable(b *bytes.Buffer, n *html.Node) error {
	kids := elementChildren(n)
	if len(kids) != 1 || kids[0].Data != "table" {
		return fmt.Errorf("body must be exactly one <table>")
	}
	var extra []string
	switch sort := attrVal(n, "sort"); sort {
	case "":
	case "on":
		extra = []string{"data-kapi-sort", "on"}
	default:
		return fmt.Errorf("sort must be on or left out, not %q", sort)
	}
	writeStart(b, "div", outerAttrs(n, "kapi-table", extra...))
	renderNode(b, kids[0])
	writeEnd(b, "div")
	return nil
}

func renderCompare(b *bytes.Buffer, n *html.Node) error {
	writeStart(b, "div", outerAttrs(n, "kapi-compare"))
	renderChildren(b, n)
	writeEnd(b, "div")
	return nil
}

func renderOption(b *bytes.Buffer, n *html.Node) error {
	title, err := requireAttr(n, "title")
	if err != nil {
		return err
	}
	writeStart(b, "section", outerAttrs(n, "kapi-option"))
	b.WriteString(`<h4 class="kapi-option-title">`)
	writeText(b, title)
	b.WriteString(`</h4><div class="kapi-option-body">`)
	renderChildren(b, n)
	b.WriteString("</div></section>")
	return nil
}

func renderPlan(b *bytes.Buffer, n *html.Node) error {
	writeStart(b, "div", outerAttrs(n, "kapi-plan"))
	renderChildren(b, n)
	writeEnd(b, "div")
	return nil
}

func planItem(class, label string) presetFunc {
	return func(b *bytes.Buffer, n *html.Node) error {
		writeStart(b, "div", outerAttrs(n, "kapi-plan-item "+class))
		b.WriteString(`<span class="kapi-plan-mark">` + label + `</span><div class="kapi-plan-body">`)
		renderChildren(b, n)
		b.WriteString("</div></div>")
		return nil
	}
}

func renderForm(b *bytes.Buffer, n *html.Node) error {
	name, err := requireAttr(n, "name")
	if err != nil {
		return err
	}
	attrs := outerAttrs(n, "kapi-form", "name", name, "data-kapi-form", name)
	writeStart(b, "form", attrs)
	renderChildren(b, n)
	b.WriteString(`<div class="kapi-form-actions"><button type="submit">Send answers</button></div></form>`)
	return nil
}

func renderChoice(b *bytes.Buffer, n *html.Node) error {
	name, err := requireAttr(n, "name")
	if err != nil {
		return err
	}
	_, multiple := attr(n, "multiple")
	kind := "single"
	if multiple {
		kind = "multiple"
	}
	writeStart(b, "fieldset", outerAttrs(n, "kapi-choice", "data-kapi-field", name, "data-kapi-choice", kind))
	var legend, opts []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "kapi-opt" {
			opts = append(opts, c)
		} else {
			legend = append(legend, c)
		}
	}
	if len(opts) == 0 {
		return fmt.Errorf("needs at least one <kapi-opt value=\"...\">")
	}
	var lb bytes.Buffer
	for _, c := range legend {
		renderNode(&lb, c)
	}
	if text := bytes.TrimSpace(lb.Bytes()); len(text) > 0 {
		b.WriteString("<legend>")
		b.Write(text)
		b.WriteString("</legend>")
	}
	b.WriteString(`<div class="kapi-choice-opts">`)
	for _, c := range opts {
		renderNode(b, c)
	}
	b.WriteString("</div></fieldset>")
	return nil
}

func renderOpt(b *bytes.Buffer, n *html.Node) error {
	value, err := requireAttr(n, "value")
	if err != nil {
		return err
	}
	name, err := requireAttr(n.Parent, "name")
	if err != nil {
		return fmt.Errorf("its <kapi-choice> %w", err)
	}
	kind := "radio"
	if _, multiple := attr(n.Parent, "multiple"); multiple {
		kind = "checkbox"
	}
	writeStart(b, "label", outerAttrs(n, "kapi-opt"))
	writeStart(b, "input", kv("type", kind, "name", name, "value", value))
	b.WriteString("<span>")
	if hasContent(n) {
		renderChildren(b, n)
	} else {
		writeText(b, value)
	}
	b.WriteString("</span></label>")
	return nil
}

func renderTextField(b *bytes.Buffer, n *html.Node) error {
	name, err := requireAttr(n, "name")
	if err != nil {
		return err
	}
	writeStart(b, "label", outerAttrs(n, "kapi-text", "data-kapi-field", name))
	b.WriteString(`<span class="kapi-text-label">`)
	renderChildren(b, n)
	b.WriteString("</span>")
	writeStart(b, "textarea", kv("name", name, "rows", "3"))
	b.WriteString("</textarea></label>")
	return nil
}
