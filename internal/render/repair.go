package render

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type noter func(n *html.Node, format string, args ...any)

type rule func(n *html.Node, note noter) *html.Node

var rules = []rule{mermaidBlock, aliasElement, nestedForm, misplaced, plainInside, looseContent, titleLead, attrAliases, enumValues, unrenderable}

const repairPasses = 16

func repair(root *html.Node) []string {
	var notes []string
	note := func(n *html.Node, format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if id := nearestID(n); id != "" {
			msg = "#" + id + " " + msg
		}
		notes = append(notes, msg)
	}
	for range repairPasses {
		noted, changed := len(notes), false
		for _, n := range repairable(root) {
			if !live(root, n) {
				continue
			}
			for _, r := range rules {
				next := r(n, note)
				changed = changed || next != n
				if n = next; n == nil {
					break
				}
			}
			if n != nil && blockPresets[n.Data] {
				parent := n.Parent
				hoist(n)
				changed = changed || n.Parent != parent
			}
		}
		if !changed && len(notes) == noted {
			break
		}
	}
	return notes
}

func repairable(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Namespace != "" || dropped[c.Data] {
			continue
		}
		out = append(out, c)
		if !rawPresets[c.Data] {
			out = append(out, repairable(c)...)
		}
	}
	return out
}

func live(root, n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p == root {
			return true
		}
		if rawPresets[p.Data] {
			return false
		}
	}
	return false
}

func nearestID(n *html.Node) string {
	for p := n; p != nil; p = p.Parent {
		if id := attrVal(p, "data-kapi-id"); id != "" {
			return id
		}
	}
	return ""
}

func content(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if !blank(c) {
			out = append(out, c)
		}
	}
	return out
}

func blank(n *html.Node) bool {
	return n.Type == html.CommentNode || (n.Type == html.TextNode && strings.TrimSpace(n.Data) == "")
}

func isElement(n *html.Node, tags ...string) bool {
	return n.Type == html.ElementNode && slices.Contains(tags, n.Data)
}

func element(tag string, a atom.Atom, attrs []html.Attribute, kids ...*html.Node) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag, DataAtom: a, Attr: attrs}
	for _, k := range kids {
		n.AppendChild(k)
	}
	return n
}

func textNode(s string) *html.Node {
	return &html.Node{Type: html.TextNode, Data: s}
}

func dataAttrs(n *html.Node) []html.Attribute {
	var out []html.Attribute
	for _, a := range n.Attr {
		if a.Namespace == "" && isDataAttr(a.Key) {
			out = append(out, a)
		}
	}
	return out
}

func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Namespace == "" && n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	n.Attr = slices.DeleteFunc(n.Attr, func(a html.Attribute) bool { return a.Namespace == "" && a.Key == key })
}

func replaceNode(n, with *html.Node) {
	n.Parent.InsertBefore(with, n)
	n.Parent.RemoveChild(n)
}

func unwrap(n *html.Node) *html.Node {
	if id := attrVal(n, "data-kapi-id"); id != "" {
		kids := content(n)
		if len(kids) != 1 || kids[0].Type != html.ElementNode || attrVal(kids[0], "data-kapi-id") != "" {
			n.Data, n.DataAtom, n.Attr = "div", atom.Div, kv("data-kapi-id", id)
			if inPhrasing(n) {
				n.Data, n.DataAtom = "span", atom.Span
			}
			return n
		}
		setAttr(kids[0], "data-kapi-id", id)
	}
	for c := n.FirstChild; c != nil; c = n.FirstChild {
		n.RemoveChild(c)
		n.Parent.InsertBefore(c, n)
	}
	n.Parent.RemoveChild(n)
	return nil
}

func mermaidBlock(n *html.Node, note noter) *html.Node {
	if n.Data != "pre" {
		return n
	}
	src, ok := mermaidPre(n)
	if !ok {
		return n
	}
	err := mermaidError(src)
	if err == nil {
		return n
	}
	note(n, "Mermaid in a <pre> %v: showed it as a plain code block", err)
	pre := element("pre", atom.Pre, dataAttrs(n), textNode(src))
	replaceNode(n, pre)
	return pre
}

func mermaidPre(n *html.Node) (string, bool) {
	if slices.Contains(strings.Fields(attrVal(n, "class")), "mermaid") {
		return rawText(n), true
	}
	kids := content(n)
	if len(kids) == 1 && isElement(kids[0], "code") && slices.Contains(strings.Fields(attrVal(kids[0], "class")), "language-mermaid") {
		return rawText(kids[0]), true
	}
	return "", false
}

type alias struct{ tag, key, val string }

var elementAliases = map[string]alias{
	"kapi-note":         {"kapi-callout", "kind", "note"},
	"kapi-info":         {"kapi-callout", "kind", "note"},
	"kapi-tip":          {"kapi-callout", "kind", "note"},
	"kapi-hint":         {"kapi-callout", "kind", "note"},
	"kapi-warning":      {"kapi-callout", "kind", "warning"},
	"kapi-warn":         {"kapi-callout", "kind", "warning"},
	"kapi-caution":      {"kapi-callout", "kind", "warning"},
	"kapi-danger":       {"kapi-callout", "kind", "danger"},
	"kapi-alert":        {"kapi-callout", "kind", "danger"},
	"kapi-error":        {"kapi-callout", "kind", "danger"},
	"kapi-success":      {"kapi-callout", "kind", "success"},
	"kapi-key":          {"kapi-callout", "kind", "key"},
	"kapi-important":    {"kapi-callout", "kind", "key"},
	"kapi-mermaid":      {"kapi-diagram", "", ""},
	"kapi-flow":         {"kapi-diagram", "type", "flow"},
	"kapi-flowchart":    {"kapi-diagram", "type", "flow"},
	"kapi-sequence":     {"kapi-diagram", "type", "sequence"},
	"kapi-architecture": {"kapi-diagram", "type", "architecture"},
	"kapi-steps":        {"kapi-plan", "", ""},
	"kapi-tasks":        {"kapi-checklist", "", ""},
	"kapi-todo":         {"kapi-task", "", ""},
}

func aliasElement(n *html.Node, note noter) *html.Node {
	if !isPreset(n) || presets[n.Data] != nil {
		return n
	}
	a, ok := elementAliases[n.Data]
	if !ok {
		note(n, "<%s> is not a preset: rendered its content as plain HTML", n.Data)
		return unwrap(n)
	}
	drawn := a.tag
	if a.key != "" {
		drawn += fmt.Sprintf(" %s=%q", a.key, a.val)
	}
	note(n, "<%s> is not a preset: drew it as <%s>", n.Data, drawn)
	n.Data = a.tag
	if a.key != "" {
		setAttr(n, a.key, a.val)
	}
	return n
}

func nestedForm(n *html.Node, note noter) *html.Node {
	if n.Data != "kapi-form" {
		return n
	}
	for a := n.Parent; a != nil; a = a.Parent {
		if a.Data == "kapi-form" {
			note(n, "<kapi-form> inside another <kapi-form>: merged its fields into the outer form")
			return unwrap(n)
		}
	}
	return n
}

var wrappers = set("kapi-plan", "kapi-kv", "kapi-checklist", "kapi-compare")

func misplaced(n *html.Node, note noter) *html.Node {
	want := parents[n.Data]
	if want == "" || n.Parent.Data == want {
		return n
	}
	for a := n.Parent; a != nil; a = a.Parent {
		if a.Data != want {
			continue
		}
		c := n
		for c.Parent != a {
			c = c.Parent
		}
		at := c.NextSibling
		for _, m := range strays(c, want) {
			note(m, "<%s> inside <%s>: moved it out into its <%s>", m.Data, m.Parent.Data, want)
			m.Parent.RemoveChild(m)
			a.InsertBefore(m, at)
		}
		return n
	}
	if !wrappers[want] {
		note(n, "<%s> outside a <%s>: rendered its content as plain HTML", n.Data, want)
		return unwrap(n)
	}
	note(n, "<%s> outside a <%s>: wrapped it in one", n.Data, want)
	w := element(want, 0, nil)
	n.Parent.InsertBefore(w, n)
	for c := n; c != nil && (parents[c.Data] == want || blank(c)); {
		next := c.NextSibling
		c.Parent.RemoveChild(c)
		w.AppendChild(c)
		c = next
	}
	for w.LastChild != nil && blank(w.LastChild) {
		c := w.LastChild
		w.RemoveChild(c)
		w.Parent.InsertBefore(c, w.NextSibling)
	}
	return w
}

func strays(n *html.Node, want string) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type != html.ElementNode || rawPresets[c.Data] || c.Data == want:
		case parents[c.Data] == want:
			out = append(out, c)
		default:
			out = append(out, strays(c, want)...)
		}
	}
	return out
}

func plainInside(n *html.Node, note noter) *html.Node {
	kids := content(n)
	switch {
	case n.Data == "kapi-kv" && len(kids) == 1 && isElement(kids[0], "dl"):
		note(n, "<kapi-kv> around a <dl>: rendered the <dl>")
		return unwrap(n)
	case n.Data == "kapi-kv" && len(kids) > 0 && !slices.ContainsFunc(kids, func(c *html.Node) bool { return !isElement(c, "dt", "dd") }):
		note(n, "<kapi-kv> holding <dt> and <dd>: rendered them as a <dl>")
		n.Data, n.DataAtom = "dl", atom.Dl
		return n
	case n.Data == "kapi-checklist" && len(kids) == 1 && isElement(kids[0], "ul", "ol"):
		note(n, "<kapi-checklist> around a <%s>: rendered it as a checklist <ul>", kids[0].Data)
		list := kids[0]
		list.Data, list.DataAtom = "ul", atom.Ul
		for _, li := range content(list) {
			if isElement(li, "li") && nativeTaskBox(li) == nil {
				li.InsertBefore(element("input", atom.Input, kv("type", "checkbox")), li.FirstChild)
			}
		}
		return unwrap(n)
	case n.Data == "kapi-details" && len(kids) == 1 && isElement(kids[0], "details"):
		note(n, "<kapi-details> around a <details>: rendered the <details>")
		if summary := attrVal(n, "summary"); summary != "" && detailsSummary(kids[0]) == nil {
			kids[0].InsertBefore(element("summary", atom.Summary, nil, textNode(summary)), kids[0].FirstChild)
		}
		return unwrap(n)
	}
	return n
}

func looseContent(n *html.Node, note noter) *html.Node {
	switch n.Data {
	case "kapi-plan":
		wrapRuns(n, "kapi-step", note)
	case "kapi-checklist":
		for _, c := range content(n) {
			if isElement(c, "li") {
				note(c, "<li> inside <kapi-checklist>: rendered it as a <kapi-task>")
				c.Data, c.DataAtom = "kapi-task", 0
				if box := nativeTaskBox(c); box != nil {
					if _, checked := attr(box, "checked"); checked {
						setAttr(c, "done", "")
					}
					c.RemoveChild(box)
				}
			}
		}
		wrapRuns(n, "kapi-task", note)
		evict(n, func(c *html.Node) bool { return isElement(c, "kapi-task") }, note)
	case "kapi-kv":
		evict(n, func(c *html.Node) bool { return isElement(c, "kapi-item") }, note)
	case "kapi-compare":
		evict(n, func(c *html.Node) bool { return c.Type == html.ElementNode && !inline(c) }, note)
	case "kapi-table":
		var table *html.Node
		for _, c := range content(n) {
			if isElement(c, "table") {
				table = c
				break
			}
		}
		if table != nil {
			evict(n, func(c *html.Node) bool { return c == table }, note)
		}
	}
	return n
}

func runs(n *html.Node) [][]*html.Node {
	var out [][]*html.Node
	var run []*html.Node
	flush := func() {
		if slices.ContainsFunc(run, func(c *html.Node) bool { return !blank(c) }) {
			out = append(out, run)
		}
		run = nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if inline(c) {
			run = append(run, c)
			continue
		}
		flush()
	}
	flush()
	return out
}

func inline(n *html.Node) bool {
	switch n.Type {
	case html.TextNode:
		return true
	case html.ElementNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && !inline(c) {
				return false
			}
		}
		return !mdBlock(n)
	}
	return false
}

func wrapRuns(n *html.Node, tag string, note noter) {
	for _, run := range runs(n) {
		note(n, "loose text inside <%s>: wrapped it in a <%s>", n.Data, tag)
		w := element(tag, 0, nil)
		n.InsertBefore(w, run[0])
		for _, c := range run {
			n.RemoveChild(c)
			w.AppendChild(c)
		}
	}
}

func evict(n *html.Node, keep func(*html.Node) bool, note noter) {
	runOf, inRun := map[*html.Node][]*html.Node{}, map[*html.Node]bool{}
	for _, run := range runs(n) {
		runOf[run[0]] = run
		for _, c := range run {
			inRun[c] = true
		}
	}
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}
	after, kept := n.NextSibling, false
	place := func(c *html.Node) {
		if kept {
			n.Parent.InsertBefore(c, after)
		} else {
			n.Parent.InsertBefore(c, n)
		}
	}
	for _, c := range kids {
		switch {
		case keep(c):
			kept = true
		case runOf[c] != nil:
			note(n, "loose text inside <%s>: moved it into a <p> beside it", n.Data)
			p := element("p", atom.P, nil)
			for _, r := range runOf[c] {
				n.RemoveChild(r)
				p.AppendChild(r)
			}
			place(p)
		case inRun[c] || c.Type != html.ElementNode:
		default:
			note(c, "<%s> inside <%s>: moved it out beside it", c.Data, n.Data)
			n.RemoveChild(c)
			place(c)
		}
	}
}

var leadTitles = set("kapi-step", "kapi-risk", "kapi-question", "kapi-task", "kapi-callout")

func titleLead(n *html.Node, note noter) *html.Node {
	title, ok := attr(n, "title")
	if !leadTitles[n.Data] || !ok {
		return n
	}
	removeAttr(n, "title")
	if title = strings.TrimSpace(title); title == "" {
		note(n, "empty title on <%s>: dropped it", n.Data)
		return n
	}
	note(n, "title on <%s>: showed it as a bold lead-in", n.Data)
	host := n
	if kids := content(n); len(kids) > 0 && isElement(kids[0], "p") {
		host = kids[0]
	}
	at := host.FirstChild
	host.InsertBefore(element("strong", atom.Strong, nil, textNode(title)), at)
	host.InsertBefore(textNode(" "), at)
	return n
}

func attrAliases(n *html.Node, note noter) *html.Node {
	switch n.Data {
	case "kapi-item":
		renameAttr(n, "title", "key", note)
	case "kapi-details":
		renameAttr(n, "title", "summary", note)
	case "kapi-opt":
		if label := nodeText(n); attrVal(n, "value") == "" && label != "" {
			note(n, "<kapi-opt> without a value: used its label %q", label)
			setAttr(n, "value", label)
		}
	}
	return n
}

func renameAttr(n *html.Node, from, to string, note noter) {
	if v := attrVal(n, from); v != "" && attrVal(n, to) == "" {
		note(n, "%s on <%s>: used it as its %s", from, n.Data, to)
		setAttr(n, to, v)
		removeAttr(n, from)
	}
}

var calloutAliases = map[string]string{
	"tip": "note", "hint": "note", "information": "note", "fyi": "note", "aside": "note",
	"important": "key", "highlight": "key", "keypoint": "key", "key-point": "key",
	"attention": "warning", "careful": "warning", "notice": "warning",
	"bad": "danger", "fail": "danger", "failure": "danger", "stop": "danger",
	"good": "success", "pass": "success", "passed": "success", "positive": "success",
}

var stateAliases = map[string]string{
	"complete": "done", "completed": "done", "finished": "done", "shipped": "done", "merged": "done", "ok": "done", "success": "done", "pass": "done", "passed": "done", "yes": "done",
	"fail": "failed", "error": "failed", "broken": "failed",
	"stuck": "blocked", "blocker": "blocked", "hold": "blocked", "on-hold": "blocked",
	"doing": "progress", "wip": "progress", "active": "progress", "started": "progress", "running": "progress", "ongoing": "progress", "in-progress": "progress", "in_progress": "progress", "inprogress": "progress",
	"todo": "waiting", "pending": "waiting", "queued": "waiting", "planned": "waiting", "next": "waiting", "later": "waiting", "not-started": "waiting",
	"note": "info", "fyi": "info", "information": "info",
}

var diagramTypes = map[string]string{
	"graph": "flow", "flowchart": "flow", "flow-chart": "flow", "chart": "flow", "dag": "flow",
	"seq": "sequence", "sequencediagram": "sequence", "sequence-diagram": "sequence",
	"arch": "architecture", "system": "architecture",
}

var dirAliases = map[string]string{
	"td": "tb", "down": "tb", "vertical": "tb", "top-down": "tb", "top-bottom": "tb", "topdown": "tb", "bt": "tb", "up": "tb",
	"right": "lr", "left": "lr", "horizontal": "lr", "left-right": "lr", "rl": "lr",
}

func enumValues(n *html.Node, note noter) *html.Node {
	switch n.Data {
	case "kapi-callout":
		fixEnum(n, "kind", func(v string) bool { return calloutKinds[calloutAlias(v)].label != "" }, calloutAliases, note)
	case "kapi-step", "kapi-risk", "kapi-question", "kapi-badge":
		fixEnum(n, "state", func(v string) bool { return v == "" || statusIcons[v].label != "" }, stateAliases, note)
	case "kapi-diagram":
		fixEnum(n, "type", func(v string) bool { _, ok := defaultDir[v]; return ok || v == "" }, diagramTypes, note)
		fixEnum(n, "dir", func(v string) bool { return v == "" || v == "tb" || v == "lr" }, dirAliases, note)
	case "kapi-code":
		if _, err := firstLine(attrVal(n, "lines")); err != nil {
			note(n, "<kapi-code> %v: numbered it from 1", err)
			removeAttr(n, "lines")
		}
	}
	return n
}

func fixEnum(n *html.Node, key string, valid func(string) bool, aliases map[string]string, note noter) {
	v, ok := attr(n, key)
	if !ok || valid(v) {
		return
	}
	to := strings.ToLower(strings.TrimSpace(v))
	if !valid(to) {
		to = aliases[to]
	}
	if to == "" {
		note(n, "%s=%q on <%s> is not one kapi knows: used the default", key, v, n.Data)
		removeAttr(n, key)
		return
	}
	note(n, "%s=%q on <%s>: used %q", key, v, n.Data, to)
	setAttr(n, key, to)
}

func unrenderable(n *html.Node, note noter) *html.Node {
	if !isPreset(n) || presets[n.Data] == nil {
		return n
	}
	var discard bytes.Buffer
	err := tryPreset(&discard, n)
	if err == nil {
		return n
	}
	if rawPresets[n.Data] {
		note(n, "<%s> %v: showed it as a plain code block", n.Data, err)
		pre := element("pre", atom.Pre, dataAttrs(n), textNode(dedent(trimBlankLines(rawText(n)))))
		replaceNode(n, pre)
		return pre
	}
	note(n, "<%s> %v: rendered its content as plain HTML", n.Data, err)
	return unwrap(n)
}
