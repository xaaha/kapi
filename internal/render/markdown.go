package render

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

func Markdown(src []byte, image func(src string) string) ([]byte, error) {
	root, err := load(src)
	if err != nil {
		return nil, err
	}
	m := &mdWriter{image: image}
	out := joinBlocks(m.blocks(root))
	if out == "" {
		return nil, nil
	}
	return []byte(out + "\n"), nil
}

type mdWriter struct {
	image func(string) string
}

var mdBlockTags = set("p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "pre", "blockquote", "table", "figure",
	"figcaption", "hr", "div", "section", "article", "aside", "header", "footer", "details", "summary", "dl", "dt", "dd",
	"caption", "main", "nav", "address", "thead", "tbody", "tfoot", "tr", "colgroup")

func mdBlock(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if isPreset(n) {
		return !inlinePresets[n.Data]
	}
	return mdBlockTags[n.Data]
}

func skipped(n *html.Node) bool {
	return n.Type == html.CommentNode || (n.Type == html.ElementNode && (n.Namespace != "" || dropped[n.Data]))
}

func (m *mdWriter) blocks(n *html.Node) []string {
	return m.blocksExcept(n, "")
}

func (m *mdWriter) block(n *html.Node) []string {
	if isPreset(n) {
		if !presetRenders(n) {
			if rawPresets[n.Data] {
				return nonEmpty(fenceOrEmpty(dedent(trimBlankLines(rawText(n)))))
			}
			return m.blocks(n)
		}
		return m.preset(n)
	}
	switch n.Data {
	case "p":
		return nonEmpty(paragraph(m.inlines(n, false)))
	case "h1", "h2", "h3", "h4", "h5", "h6":
		text := oneLine(m.inlines(n, false))
		if text == "" {
			return nil
		}
		return []string{strings.Repeat("#", int(n.Data[1]-'0')) + " " + text}
	case "ul", "ol":
		return nonEmpty(m.list(n))
	case "li":
		return nonEmpty(listItem("- ", m.blocks(n)))
	case "pre":
		return []string{fence(lang(n), plainText(n))}
	case "blockquote":
		return nonEmpty(quote(m.blocks(n)))
	case "hr":
		return []string{"---"}
	case "table":
		return m.table(n)
	case "figcaption", "caption", "summary", "dt":
		return nonEmpty(paragraph(emphasis("*", m.inlines(n, false))))
	case "details":
		return m.details(summaryText(n), m.blocksExcept(n, "summary"))
	case "dl":
		return nonEmpty(m.definitions(n))
	}
	return m.blocks(n)
}

func (m *mdWriter) blocksExcept(n *html.Node, tag string) []string {
	var out, run []string
	flush := func() {
		if p := paragraph(strings.Join(run, "")); p != "" {
			out = append(out, p)
		}
		run = nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case skipped(c), c.Type == html.ElementNode && c.Data == tag:
		case mdBlock(c):
			flush()
			out = append(out, m.block(c)...)
		default:
			run = append(run, m.inline(c, false))
		}
	}
	flush()
	return out
}

func summaryText(n *html.Node) string {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "summary" {
			return nodeText(c)
		}
	}
	return "Details"
}

func (m *mdWriter) details(summary string, body []string) []string {
	out := []string{"<details>\n<summary>" + escText(summary) + "</summary>"}
	out = append(out, body...)
	return append(out, "</details>")
}

func (m *mdWriter) list(n *html.Node) string {
	ordered := n.Data == "ol"
	num := 1
	if v, err := strconv.Atoi(attrVal(n, "start")); ordered && err == nil {
		num = v
	}
	var items []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if skipped(c) || (c.Type == html.TextNode && strings.TrimSpace(c.Data) == "") {
			continue
		}
		var body []string
		switch {
		case c.Type == html.ElementNode && c.Data == "li":
			body = m.blocks(c)
			if v, err := strconv.Atoi(attrVal(c, "value")); ordered && err == nil {
				num = v
			}
		case mdBlock(c):
			body = m.block(c)
		default:
			body = nonEmpty(paragraph(m.inline(c, false)))
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(num) + ". "
			num++
		}
		if item := listItem(marker, body); item != "" {
			items = append(items, item)
		}
	}
	return strings.Join(items, "\n")
}

func listItem(marker string, body []string) string {
	text := joinBlocks(body)
	if text == "" {
		text = " "
	}
	return prefixLines(text, marker, strings.Repeat(" ", len(marker)))
}

func quote(body []string) string {
	if len(body) == 0 {
		return ""
	}
	return prefixLines(joinBlocks(body), "> ", "> ")
}

var listStart = regexp.MustCompile(`^([-*+]|\d+[.)])( |$)`)

func listKind(blk string) string {
	m := listStart.FindStringSubmatch(blk)
	switch {
	case m == nil:
		return ""
	case strings.ContainsAny(m[1], "-*+"):
		return "ul"
	}
	return "ol"
}

func joinBlocks(blocks []string) string {
	var b strings.Builder
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString("\n\n")
			if kind := listKind(blk); kind != "" && kind == listKind(blocks[i-1]) {
				b.WriteString("<!-- -->\n\n")
			}
		}
		b.WriteString(blk)
	}
	return b.String()
}

func prefixLines(text, first, rest string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		p := rest
		if i == 0 {
			p = first
		}
		if l == "" {
			p = strings.TrimRight(p, " ")
		}
		lines[i] = p + l
	}
	return strings.Join(lines, "\n")
}

func (m *mdWriter) definitions(n *html.Node) string {
	var items []string
	var term string
	var body []string
	flush := func() {
		if term != "" || len(body) > 0 {
			items = append(items, definition(term, body))
		}
		term, body = "", nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || skipped(c) {
			continue
		}
		switch c.Data {
		case "dt":
			flush()
			term = oneLine(m.inlines(c, false))
		case "dd":
			body = append(body, m.blocks(c)...)
		default:
			body = append(body, m.block(c)...)
		}
	}
	flush()
	return strings.Join(items, "\n")
}

func definition(term string, body []string) string {
	head := "- "
	if term != "" {
		head += "**" + term + "**"
	}
	if len(body) == 1 && !strings.Contains(body[0], "\n") && !mdStructural.MatchString(body[0]) {
		if term != "" {
			head += ": "
		}
		return head + body[0]
	}
	if len(body) == 0 {
		return head
	}
	return head + "\n\n" + prefixLines(joinBlocks(body), "  ", "  ")
}

var mdStructural = regexp.MustCompile("^(```|<details>|[-*+] |\\d+\\. |> |\\|)")

func (m *mdWriter) table(n *html.Node) []string {
	var rows [][]string
	var caption []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || skipped(c) {
				continue
			}
			switch c.Data {
			case "caption":
				caption = m.block(c)
			case "tr":
				var row []string
				header := c.Parent.Data == "thead"
				for td := c.FirstChild; td != nil; td = td.NextSibling {
					if td.Type != html.ElementNode || (td.Data != "td" && td.Data != "th") {
						continue
					}
					header = header || td.Data == "th"
					row = append(row, m.cell(td))
					if span, err := strconv.Atoi(attrVal(td, "colspan")); err == nil {
						for i := 1; i < min(span, 1000); i++ {
							row = append(row, "")
						}
					}
				}
				if len(rows) == 0 && !header {
					rows = append(rows, nil)
				}
				rows = append(rows, row)
			case "thead", "tbody", "tfoot":
				walk(c)
			}
		}
	}
	walk(n)
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	if width == 0 {
		return caption
	}
	var b strings.Builder
	line := func(cells []string) {
		b.WriteString("|")
		for i := range width {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			b.WriteString(" " + cell + " |")
		}
		b.WriteString("\n")
	}
	line(rows[0])
	b.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
	for _, r := range rows[1:] {
		line(r)
	}
	return append(caption, strings.TrimSuffix(b.String(), "\n"))
}

func (m *mdWriter) cell(n *html.Node) string {
	var parts []string
	var run []string
	flush := func() {
		if s := collapse(strings.Join(run, "")); s != "" {
			parts = append(parts, s)
		}
		run = nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case skipped(c):
		case mdBlock(c):
			flush()
			for _, blk := range m.block(c) {
				parts = append(parts, strings.ReplaceAll(blk, "\n", "<br>"))
			}
		default:
			run = append(run, m.inline(c, true))
		}
	}
	flush()
	return strings.ReplaceAll(strings.Join(parts, "<br>"), "|", `\|`)
}

func (m *mdWriter) inlines(n *html.Node, cell bool) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(m.inline(c, cell))
	}
	return b.String()
}

func (m *mdWriter) inline(n *html.Node, cell bool) string {
	switch n.Type {
	case html.TextNode:
		return escapeMD(whitespace.ReplaceAllString(n.Data, " "))
	case html.ElementNode:
	default:
		return ""
	}
	if skipped(n) {
		return ""
	}
	if mdBlock(n) {
		return " " + strings.Join(m.block(n), " ") + " "
	}
	switch n.Data {
	case "kapi-badge":
		return m.badge(n)
	case "em", "i", "cite", "dfn", "var":
		return emphasis("*", m.inlines(n, cell))
	case "strong", "b":
		return emphasis("**", m.inlines(n, cell))
	case "del", "s":
		return emphasis("~~", m.inlines(n, cell))
	case "code", "kbd", "samp":
		return codeSpan(whitespace.ReplaceAllString(plainText(n), " "))
	case "q":
		return `"` + m.inlines(n, cell) + `"`
	case "br":
		if cell {
			return "<br>"
		}
		return "\\\n"
	case "img":
		return m.img(n)
	case "a":
		text := m.inlines(n, cell)
		href, ok := attr(n, "href")
		if !ok || href == "" {
			return text
		}
		if strings.TrimSpace(text) == "" {
			text = escapeMD(href)
		}
		return "[" + text + "](" + mdURL(href) + ")"
	}
	return m.inlines(n, cell)
}

func (m *mdWriter) img(n *html.Node) string {
	src, ok := attr(n, "src")
	alt := escapeMD(whitespace.ReplaceAllString(attrVal(n, "alt"), " "))
	if !ok || src == "" {
		return alt
	}
	if m.image != nil {
		src = m.image(src)
	}
	return "![" + alt + "](" + mdURL(src) + ")"
}

var stateMarks = map[string]string{
	"done":     "✅",
	"failed":   "❌",
	"blocked":  "⛔",
	"progress": "\U0001f504",
	"waiting":  "⏳",
	"info":     "ℹ️",
}

func (m *mdWriter) badge(n *html.Node) string {
	text := strings.TrimSpace(m.inlines(n, false))
	mark := stateMarks[attrVal(n, "state")]
	switch {
	case mark == "":
		return codeSpan(nodeText(n))
	case text == "":
		return mark
	}
	return mark + " " + codeSpan(nodeText(n))
}

func (m *mdWriter) preset(n *html.Node) []string {
	switch n.Data {
	case "kapi-callout":
		kind := attrVal(n, "kind")
		if kind == "" {
			kind = "note"
		}
		return []string{prefixLines("[!"+alertKinds[kind]+"]\n"+joinBlocks(m.blocks(n)), "> ", "> ")}
	case "kapi-code":
		var out []string
		if label := codeLabel(attrVal(n, "file"), attrVal(n, "lines")); label != "" {
			out = append(out, label)
		}
		return append(out, fence(codeLang(attrVal(n, "lang"), attrVal(n, "file")), dedent(trimBlankLines(rawText(n)))))
	case "kapi-diff":
		var out []string
		if label := codeLabel(attrVal(n, "file"), ""); label != "" {
			out = append(out, label)
		}
		return append(out, fence("diff", diffDedent(trimBlankLines(rawText(n)))))
	case "kapi-diagram":
		info := "kapi-diagram"
		for _, k := range []string{"type", "dir"} {
			if v := attrVal(n, k); v != "" {
				info += " " + k + "=" + v
			}
		}
		return []string{fence(info, dedent(trimBlankLines(rawText(n))))}
	case "kapi-table":
		if kids := elementChildren(n); len(kids) > 0 {
			return m.table(kids[0])
		}
	case "kapi-option":
		return append([]string{"**" + escapeMD(oneLine(attrVal(n, "title"))) + "**"}, m.blocks(n)...)
	case "kapi-plan":
		return nonEmpty(m.plan(n))
	case "kapi-form":
		return m.blocks(n)
	case "kapi-choice":
		return m.choice(n)
	case "kapi-text":
		return nonEmpty(paragraph(strings.TrimSpace(m.inlines(n, false)) + " " + emphasis("*", "(free text)")))
	case "kapi-kv":
		var items []string
		for _, c := range elementChildren(n) {
			items = append(items, definition(escapeMD(oneLine(attrVal(c, "key"))), m.blocks(c)))
		}
		return nonEmpty(strings.Join(items, "\n"))
	case "kapi-details":
		return m.details(attrVal(n, "summary"), m.blocks(n))
	case "kapi-checklist":
		var items []string
		for _, c := range elementChildren(n) {
			box := "- [ ] "
			if _, done := attr(c, "done"); done {
				box = "- [x] "
			}
			items = append(items, listItem(box, m.blocks(c)))
		}
		return nonEmpty(strings.Join(items, "\n"))
	}
	return m.blocks(n)
}

var alertKinds = map[string]string{"note": "NOTE", "warning": "WARNING", "key": "IMPORTANT", "danger": "CAUTION", "success": "TIP"}

func (m *mdWriter) plan(n *html.Node) string {
	var groups []string
	var group []string
	ordered := false
	step := 1
	for _, c := range elementChildren(n) {
		body := m.blocks(c)
		if mark := stateMarks[attrVal(c, "state")]; mark != "" && len(body) > 0 {
			body[0] = mark + " " + body[0]
		}
		var item string
		switch c.Data {
		case "kapi-step":
			item = listItem(strconv.Itoa(step)+". ", body)
			step++
		case "kapi-risk":
			item = listItem("- ", labelled("Risk", body))
		case "kapi-question":
			item = listItem("- ", labelled("Question", body))
		default:
			continue
		}
		if isStep := c.Data == "kapi-step"; len(group) > 0 && isStep != ordered {
			groups = append(groups, strings.Join(group, "\n"))
			group = nil
		}
		ordered = c.Data == "kapi-step"
		group = append(group, item)
	}
	if len(group) > 0 {
		groups = append(groups, strings.Join(group, "\n"))
	}
	return joinBlocks(groups)
}

func labelled(label string, body []string) []string {
	if len(body) == 0 {
		return []string{"**" + label + "**"}
	}
	out := append([]string(nil), body...)
	out[0] = "**" + label + ":** " + out[0]
	return out
}

func (m *mdWriter) choice(n *html.Node) []string {
	var question []string
	var opts []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "kapi-opt" {
			label := oneLine(m.inlines(c, false))
			if label == "" {
				label = escapeMD(attrVal(c, "value"))
			}
			opts = append(opts, "- [ ] "+label)
			continue
		}
		if !skipped(c) {
			question = append(question, m.inline(c, false))
		}
	}
	return append(nonEmpty(paragraph(strings.Join(question, ""))), strings.Join(opts, "\n"))
}

func codeLabel(file, lines string) string {
	if file == "" {
		return ""
	}
	label := codeSpan(file)
	if lines != "" {
		label += ", lines " + lines
	}
	return label
}

func codeLang(lang, file string) string {
	if lang != "" || file == "" {
		return lang
	}
	if ext := strings.TrimPrefix(filepath.Ext(file), "."); ext != "" {
		return ext
	}
	if l := pickLexer("", file); l != nil && len(l.Config().Aliases) > 0 {
		return l.Config().Aliases[0]
	}
	return ""
}

var languageClass = regexp.MustCompile(`(?:^|\s)(?:language|lang)-([A-Za-z0-9_+#.-]+)`)

func lang(pre *html.Node) string {
	for _, n := range append([]*html.Node{pre}, elementChildren(pre)...) {
		if m := languageClass.FindStringSubmatch(attrVal(n, "class")); m != nil {
			return m[1]
		}
	}
	return ""
}

func fenceOrEmpty(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return fence("", body)
}

func fence(info, body string) string {
	longest := 0
	for _, run := range backticks.FindAllString(body, -1) {
		longest = max(longest, len(run))
	}
	f := strings.Repeat("`", max(3, longest+1))
	return f + info + "\n" + strings.TrimSuffix(body, "\n") + "\n" + f
}

var backticks = regexp.MustCompile("`+")

func codeSpan(s string) string {
	if s == "" {
		return ""
	}
	longest := 0
	for _, run := range backticks.FindAllString(s, -1) {
		longest = max(longest, len(run))
	}
	f := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return f + s + f
}

func plainText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				b.WriteString(c.Data)
			case c.Type == html.ElementNode && c.Data == "br":
				b.WriteString("\n")
			case !skipped(c):
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

func emphasis(mark, inner string) string {
	trimmed := strings.TrimSpace(inner)
	if trimmed == "" {
		return inner
	}
	lead := inner[:len(inner)-len(strings.TrimLeft(inner, " \n"))]
	trail := inner[len(strings.TrimRight(inner, " \n")):]
	return lead + mark + trimmed + mark + trail
}

func mdURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.ContainsAny(u, " ()<>") {
		return "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(u) + ">"
	}
	return u
}

var whitespace = regexp.MustCompile(`[ \t\n\r\f]+`)

var mdSpecial = strings.NewReplacer(`\`, `\\`, "*", `\*`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`, "~", `\~`)

var entityLike = regexp.MustCompile(`&(#?[A-Za-z0-9]+;)`)

var looseUnderscore = regexp.MustCompile(`(^|[^A-Za-z0-9])_|_([^A-Za-z0-9]|$)`)

func escapeMD(s string) string {
	s = mdSpecial.Replace(s)
	s = entityLike.ReplaceAllString(s, `\&$1`)
	return looseUnderscore.ReplaceAllStringFunc(s, func(m string) string { return strings.Replace(m, "_", `\_`, 1) })
}

var leadingDigits = regexp.MustCompile(`^\d+`)

var blockStart = regexp.MustCompile(`^(#{1,6}(?:\s|$)|[-+=>|]|\d+[.)](?:\s|$))`)

func paragraph(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = collapse(l)
		if blockStart.MatchString(l) {
			if m := leadingDigits.FindString(l); m != "" {
				l = m + `\` + l[len(m):]
			} else {
				l = `\` + l
			}
		}
		lines[i] = l
	}
	out := strings.Trim(strings.Join(lines, "\n"), "\n")
	for trailingBreak(out) {
		out = strings.Trim(strings.TrimSuffix(out, `\`), " \n")
	}
	return strings.Trim(out, "\n")
}

func trailingBreak(s string) bool {
	return (len(s)-len(strings.TrimRight(s, `\`)))%2 == 1
}

func collapse(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func oneLine(s string) string {
	return collapse(strings.ReplaceAll(s, "\\\n", " "))
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
