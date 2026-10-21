package render

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"golang.org/x/net/html"
)

var linesAttr = regexp.MustCompile(`^([1-9][0-9]*)(?:-([1-9][0-9]*))?$`)

func renderCode(b *bytes.Buffer, n *html.Node) error {
	lang, file := attrVal(n, "lang"), attrVal(n, "file")
	start := 1
	if lines := attrVal(n, "lines"); lines != "" {
		m := linesAttr.FindStringSubmatch(lines)
		if m == nil {
			return fmt.Errorf("lines must look like 10-24, not %q", lines)
		}
		start, _ = strconv.Atoi(m[1])
		if end, _ := strconv.Atoi(m[2]); m[2] != "" && end < start {
			return fmt.Errorf("lines %q ends before it starts", lines)
		}
	}
	src := dedent(trimBlankLines(rawText(n)))
	if src == "" {
		return fmt.Errorf("has no source")
	}
	lines := highlight(pickLexer(lang, file), src)
	last := start + len(lines) - 1
	writeStart(b, "figure", outerAttrs(n, "kapi-code"))
	codeHead(b, file, lang)
	writeStart(b, "pre", kv("class", "kapi-hl", "data-gutter", strconv.Itoa(len(strconv.Itoa(last)))))
	b.WriteString("<code>")
	for i, line := range lines {
		b.WriteString(`<span class="kapi-line"><span class="kapi-ln" data-n="` + strconv.Itoa(start+i) + `"></span>`)
		b.WriteString(`<span class="kapi-src">` + line + "</span></span>\n")
	}
	b.WriteString("</code></pre></figure>")
	return nil
}

func codeHead(b *bytes.Buffer, file, lang string, extra ...string) {
	if file == "" && lang == "" && len(extra) == 0 {
		return
	}
	b.WriteString(`<figcaption class="kapi-code-head">`)
	if file != "" {
		b.WriteString(`<span class="kapi-code-file">`)
		writeText(b, file)
		b.WriteString("</span>")
	}
	for _, e := range extra {
		b.WriteString(e)
	}
	if lang != "" {
		b.WriteString(`<span class="kapi-code-lang">`)
		writeText(b, lang)
		b.WriteString("</span>")
	}
	b.WriteString("</figcaption>")
}

func pickLexer(lang, file string) chroma.Lexer {
	var l chroma.Lexer
	if lang != "" {
		l = lexers.Get(lang)
	}
	if l == nil && file != "" {
		l = lexers.Match(file)
	}
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}

func highlight(l chroma.Lexer, src string) []string {
	plain := strings.Split(src, "\n")
	if l == nil {
		return escapeAll(plain)
	}
	it, err := l.Tokenise(nil, src+"\n")
	if err != nil {
		return escapeAll(plain)
	}
	out := make([]string, len(plain))
	line := 0
	var cur strings.Builder
	for _, tok := range it.Tokens() {
		cls := tokenClass(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				if line < len(out) {
					out[line] = cur.String()
				}
				line++
				cur.Reset()
			}
			if part == "" {
				continue
			}
			if cls == "" {
				cur.WriteString(escText(part))
				continue
			}
			cur.WriteString(`<span class="` + cls + `">` + escText(part) + "</span>")
		}
	}
	if line < len(out) {
		out[line] = cur.String()
	}
	return out
}

func escapeAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, s := range lines {
		out[i] = escText(s)
	}
	return out
}

func tokenClass(t chroma.TokenType) string {
	for t != 0 {
		if cls, ok := chroma.StandardTypes[t]; ok {
			if cls == "" || cls == "w" {
				return ""
			}
			return "t-" + cls
		}
		t = t.Parent()
	}
	return ""
}

func trimBlankLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	prefix := ""
	first := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if first {
			prefix, first = indent, false
			continue
		}
		for !strings.HasPrefix(indent, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return stripPrefix(lines, prefix)
}

func stripPrefix(lines []string, prefix string) string {
	if prefix == "" {
		return strings.Join(lines, "\n")
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimPrefix(l, prefix)
		if strings.TrimSpace(out[i]) == "" {
			out[i] = ""
		}
	}
	return strings.Join(out, "\n")
}

type diffLine struct {
	kind     string
	old, new int
	text     string
}

var hunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@`)

var fileHeaderPrefixes = []string{"diff ", "index ", "--- ", "+++ ", "new file mode", "deleted file mode", "old mode", "new mode", "similarity index", "rename from", "rename to", "Binary files"}

func renderDiff(b *bytes.Buffer, n *html.Node) error {
	lang, file := attrVal(n, "lang"), attrVal(n, "file")
	src := diffDedent(trimBlankLines(rawText(n)))
	if src == "" {
		return fmt.Errorf("has no diff")
	}
	lines, detected := parseDiff(src)
	if file == "" {
		file = detected
	}
	added, removed := 0, 0
	var code []string
	maxN := 0
	for _, l := range lines {
		switch l.kind {
		case "add":
			added++
		case "del":
			removed++
		}
		if l.kind == "add" || l.kind == "del" || l.kind == "ctx" {
			code = append(code, l.text)
		}
		maxN = max(maxN, l.old, l.new)
	}
	var hl []string
	if len(code) > 0 {
		hl = highlight(pickLexer(lang, file), strings.Join(code, "\n"))
	}
	stat := fmt.Sprintf(`<span class="kapi-diff-stat"><span class="kapi-diff-added">+%d</span> <span class="kapi-diff-removed">&minus;%d</span></span>`, added, removed)
	writeStart(b, "figure", outerAttrs(n, "kapi-diff"))
	codeHead(b, file, lang, stat)
	writeStart(b, "pre", kv("class", "kapi-hl", "data-gutter", strconv.Itoa(len(strconv.Itoa(maxN)))))
	b.WriteString("<code>")
	ci := 0
	for _, l := range lines {
		switch l.kind {
		case "file", "hunk", "meta":
			b.WriteString(`<span class="kapi-line kapi-diff-` + l.kind + `">`)
			writeText(b, l.text)
			b.WriteString("</span>\n")
			continue
		}
		b.WriteString(`<span class="kapi-line kapi-diff-` + l.kind + `">`)
		b.WriteString(`<span class="kapi-ln" data-n="` + lineNo(l.old) + `"></span>`)
		b.WriteString(`<span class="kapi-ln" data-n="` + lineNo(l.new) + `"></span>`)
		b.WriteString(`<span class="kapi-diff-sign">` + map[string]string{"add": "+", "del": "-", "ctx": " "}[l.kind] + "</span>")
		b.WriteString(`<span class="kapi-src">` + hl[ci] + "</span></span>\n")
		ci++
	}
	b.WriteString("</code></pre></figure>")
	return nil
}

func lineNo(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func diffDedent(s string) string {
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		t := strings.TrimLeft(l, " \t")
		if strings.HasPrefix(t, "@@") || strings.HasPrefix(t, "diff ") || strings.HasPrefix(t, "--- ") {
			return stripPrefix(lines, l[:len(l)-len(t)])
		}
	}
	return s
}

func parseDiff(src string) ([]diffLine, string) {
	var out []diffLine
	var files []string
	minus := ""
	oldN, newN := 0, 0
	inHunk := false
	for _, l := range strings.Split(src, "\n") {
		if m := hunkHeader.FindStringSubmatch(l); m != nil {
			oldN, _ = strconv.Atoi(m[1])
			newN, _ = strconv.Atoi(m[2])
			inHunk = true
			out = append(out, diffLine{kind: "hunk", text: l})
			continue
		}
		if (!inHunk || strings.HasPrefix(l, "diff ")) && isFileHeader(l) {
			inHunk = false
			switch {
			case strings.HasPrefix(l, "--- "):
				minus = headerFile(l)
			case strings.HasPrefix(l, "+++ "):
				name := headerFile(l)
				if name == "" {
					name = minus
				}
				files = append(files, name)
				out = append(out, diffLine{kind: "file", text: name})
			case strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			default:
				out = append(out, diffLine{kind: "meta", text: l})
			}
			continue
		}
		switch {
		case strings.HasPrefix(l, `\`):
			out = append(out, diffLine{kind: "meta", text: l})
		case strings.HasPrefix(l, "+"):
			out = append(out, diffLine{kind: "add", new: count(&newN, inHunk), text: l[1:]})
		case strings.HasPrefix(l, "-"):
			out = append(out, diffLine{kind: "del", old: count(&oldN, inHunk), text: l[1:]})
		default:
			out = append(out, diffLine{kind: "ctx", old: count(&oldN, inHunk), new: count(&newN, inHunk), text: strings.TrimPrefix(l, " ")})
		}
	}
	if len(files) > 1 {
		return out, ""
	}
	kept := out[:0]
	for _, l := range out {
		if l.kind != "file" {
			kept = append(kept, l)
		}
	}
	if len(files) == 1 {
		return kept, files[0]
	}
	return kept, ""
}

func count(n *int, inHunk bool) int {
	if !inHunk {
		return 0
	}
	v := *n
	*n++
	return v
}

func isFileHeader(l string) bool {
	for _, p := range fileHeaderPrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func headerFile(l string) string {
	var name string
	switch {
	case strings.HasPrefix(l, "+++ "):
		name = strings.TrimPrefix(l, "+++ ")
	case strings.HasPrefix(l, "--- "):
		name = strings.TrimPrefix(l, "--- ")
	default:
		return ""
	}
	if i := strings.IndexByte(name, '\t'); i >= 0 {
		name = name[:i]
	}
	if name == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(name, "a/") || strings.HasPrefix(name, "b/") {
		name = name[2:]
	}
	return name
}
