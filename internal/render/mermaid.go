package render

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var mermaidHeaders = set("flowchart", "graph", "sequencediagram", "classdiagram", "classdiagram-v2", "statediagram", "statediagram-v2",
	"erdiagram", "journey", "gantt", "pie", "quadrantchart", "requirementdiagram", "gitgraph", "mindmap", "timeline", "zenuml",
	"c4context", "c4container", "c4component", "c4dynamic", "c4deployment", "sankey-beta", "xychart-beta", "block-beta",
	"packet-beta", "kanban", "architecture-beta", "radar-beta")

var (
	mermaidFlowHeader = regexp.MustCompile(`^(?i:flowchart|graph)(?:\s+(TB|TD|BT|RL|LR))?\s*(?:;(.*))?$`)
	mermaidID         = regexp.MustCompile(`^[\p{L}\p{N}_]+(?:-[\p{L}\p{N}_]+)*`)
	mermaidClass      = regexp.MustCompile(`^:::[\w-]+`)
	mermaidTextLink   = regexp.MustCompile(`^(<)?(--|==|-\.)\s+(.*?)\s*(-{2,}>|={2,}>|\.-+>|-{3,}|={3,}|\.-+|-{2,}[ox]|={2,}[ox])`)
	mermaidLink       = regexp.MustCompile(`^(<)?(-{2,}|={2,}|-\.+-)(>|o|x)?`)
	mermaidPipe       = regexp.MustCompile(`^\s*\|([^|]*)\|`)
	mermaidBreak      = regexp.MustCompile(`(?i)<br\s*/?>`)
	mermaidActor      = regexp.MustCompile(`^(participant|actor)\s+(.+?)(?:\s+as\s+(.+))?$`)
	mermaidMessage    = regexp.MustCompile(`^(.+?)\s*(-->>|->>|--x|-x|--\)|-\)|-->|->)[+-]?\s*([^:]+?)\s*(?::(.*))?$`)
)

var mermaidStyling = set("classdef", "class", "style", "linkstyle", "click")

var mermaidShapes = []struct{ open, close string }{
	{"(((", ")))"}, {"((", "))"}, {"([", "])"}, {"[[", "]]"}, {"[(", ")]"}, {"{{", "}}"},
	{"[/", "/]"}, {"[/", `\]`}, {`[\`, `\]`}, {`[\`, "/]"}, {"(", ")"}, {"[", "]"}, {"{", "}"}, {">", "]"},
}

var kindWords = map[string]string{
	"db": "database", "database": "database", "databases": "database", "postgres": "database", "postgresql": "database",
	"mysql": "database", "sqlite": "database", "mongodb": "database", "dynamodb": "database",
	"queue": "queue", "queues": "queue", "kafka": "queue", "sqs": "queue", "rabbitmq": "queue",
	"user": "user", "users": "user", "customer": "user", "customers": "user", "person": "user", "people": "user", "human": "user",
	"agent": "agent", "agents": "agent", "llm": "agent", "bot": "agent",
	"service": "service", "services": "service", "api": "service", "server": "service", "backend": "service", "worker": "service", "daemon": "service",
	"cron": "timer", "timer": "timer", "scheduler": "timer",
	"file": "file", "files": "file", "bucket": "file", "s3": "file",
}

func mermaidSource(src string) bool {
	line := firstMermaidLine(strings.Split(src, "\n"))
	if line < 0 {
		return false
	}
	head := strings.TrimSpace(strings.Split(src, "\n")[line])
	if mermaidFlowHeader.MatchString(head) {
		return true
	}
	words := strings.Fields(head)
	return mermaidHeaders[strings.ToLower(words[0])] && !strings.Contains(head, "->")
}

var mermaidDirection = regexp.MustCompile(`^[ \t]*(?i:flowchart|graph)(?:[ \t]+(TB|TD|BT|RL|LR)\b)?`)

func MermaidDirection(src []byte) (start, end int, ok bool) {
	at := 0
	for _, line := range bytes.SplitAfter(src, []byte("\n")) {
		if t := bytes.TrimSpace(line); len(t) > 0 && !bytes.HasPrefix(t, []byte("%%")) {
			m := mermaidDirection.FindSubmatchIndex(line)
			switch {
			case m == nil:
				return 0, 0, false
			case m[2] >= 0:
				return at + m[2], at + m[3], true
			}
			return at + m[1], at + m[1], true
		}
		at += len(line)
	}
	return 0, 0, false
}

func mermaidError(src string) error {
	if !mermaidSource(src) {
		return fmt.Errorf("has no Mermaid header such as flowchart TD or sequenceDiagram")
	}
	_, _, _, err := parseMermaid(src)
	return err
}

func firstMermaidLine(lines []string) int {
	for i, l := range lines {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "%%") {
			return i
		}
	}
	return -1
}

func parseMermaid(src string) (kind, dir string, g *graph, err error) {
	lines := strings.Split(src, "\n")
	h := firstMermaidLine(lines)
	head := strings.TrimSpace(lines[h])
	if m := mermaidFlowHeader.FindStringSubmatch(head); m != nil {
		dir = "tb"
		if d := strings.ToUpper(m[1]); d == "LR" || d == "RL" {
			dir = "lr"
		}
		lines[h] = m[2]
		g, err = parseMermaidFlow(lines, h)
		return "flow", dir, g, err
	}
	word := strings.Fields(head)[0]
	if !strings.EqualFold(word, "sequenceDiagram") {
		return "", "", nil, fmt.Errorf("is a Mermaid %s diagram, which kapi does not draw; use flowchart or sequenceDiagram", word)
	}
	g, err = parseMermaidSequence(lines, h+1)
	return "sequence", "", g, err
}

type mermaidGraph struct {
	g       *graph
	ids     []string
	labeled []bool
	index   map[string]int
}

func (m *mermaidGraph) node(id, label, open string) int {
	i, ok := m.index[id]
	if !ok {
		i = len(m.ids)
		m.index[id] = i
		m.ids = append(m.ids, id)
		m.labeled = append(m.labeled, false)
		m.g.names = append(m.g.names, id)
		m.g.looks = append(m.g.looks, nodeLook{})
	}
	if label != "" && !m.labeled[i] {
		m.g.names[i], m.labeled[i] = label, true
	}
	if open == "[(" {
		m.g.looks[i].kind = "database"
	}
	return i
}

func parseMermaidFlow(lines []string, from int) (*graph, error) {
	m := &mermaidGraph{g: &graph{}, index: map[string]int{}}
	for i := from; i < len(lines); i++ {
		for _, stmt := range strings.Split(lines[i], ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(stmt, "%%") || mermaidStyling[strings.ToLower(strings.Fields(stmt)[0])] {
				continue
			}
			if err := m.statement(stmt); err != nil {
				return nil, fmt.Errorf("line %d: %q is outside the Mermaid subset kapi draws (%w)", i+1, stmt, err)
			}
		}
	}
	if len(m.ids) == 0 {
		return nil, fmt.Errorf("has no nodes")
	}
	for i, id := range m.ids {
		if m.g.looks[i].kind == "" {
			m.g.looks[i].kind = guessKind(m.g.names[i] + " " + id)
		}
	}
	return m.g, nil
}

func (m *mermaidGraph) statement(s string) error {
	from, rest, err := m.group(s)
	if err != nil {
		return err
	}
	for rest = strings.TrimSpace(rest); rest != ""; rest = strings.TrimSpace(rest) {
		e, after, ok := parseMermaidLink(rest)
		if !ok {
			return fmt.Errorf("expected a link such as --> before %q", rest)
		}
		to, after, err := m.group(after)
		if err != nil {
			return err
		}
		for _, f := range from {
			for _, t := range to {
				e.from, e.to = f, t
				m.g.edges = append(m.g.edges, e)
			}
		}
		from, rest = to, after
	}
	return nil
}

func (m *mermaidGraph) group(s string) ([]int, string, error) {
	var out []int
	for {
		s = strings.TrimSpace(s)
		id := mermaidID.FindString(s)
		if id == "" || strings.EqualFold(id, "subgraph") || strings.EqualFold(id, "end") || strings.EqualFold(id, "direction") {
			return nil, "", fmt.Errorf("expected a node id at %q", s)
		}
		label, open, rest := mermaidShape(s[len(id):])
		out = append(out, m.node(id, label, open))
		rest = strings.TrimSpace(mermaidClass.ReplaceAllString(rest, ""))
		if !strings.HasPrefix(rest, "&") {
			return out, rest, nil
		}
		s = rest[1:]
	}
}

func mermaidShape(s string) (label, open, rest string) {
	for _, sh := range mermaidShapes {
		body, ok := strings.CutPrefix(s, sh.open)
		if !ok {
			continue
		}
		if quoted, ok := strings.CutPrefix(body, `"`); ok {
			if q := strings.IndexByte(quoted, '"'); q >= 0 {
				if after, ok := strings.CutPrefix(strings.TrimSpace(quoted[q+1:]), sh.close); ok {
					return mermaidLabel(quoted[:q]), sh.open, after
				}
			}
			continue
		}
		if at := strings.Index(body, sh.close); at >= 0 {
			return mermaidLabel(body[:at]), sh.open, body[at+len(sh.close):]
		}
	}
	return "", "", s
}

func mermaidLabel(s string) string {
	return strings.Join(strings.Fields(strings.Trim(mermaidBreak.ReplaceAllString(s, " "), `"`)), " ")
}

func parseMermaidLink(s string) (edge, string, bool) {
	if m := mermaidTextLink.FindStringSubmatch(s); m != nil {
		return edge{label: mermaidLabel(m[3]), dashed: m[2] == "-.", twoWay: m[1] != "" && strings.HasSuffix(m[4], ">")}, s[len(m[0]):], true
	}
	m := mermaidLink.FindStringSubmatch(s)
	if m == nil {
		return edge{}, "", false
	}
	e := edge{dashed: strings.Contains(m[2], "."), twoWay: m[1] != "" && m[3] == ">"}
	rest := s[len(m[0]):]
	if p := mermaidPipe.FindStringSubmatch(rest); p != nil {
		e.label = mermaidLabel(p[1])
		rest = rest[len(p[0]):]
	}
	return e, rest, true
}

func guessKind(words string) string {
	for _, w := range strings.FieldsFunc(strings.ToLower(words), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if k := kindWords[w]; k != "" {
			return k
		}
	}
	return ""
}

func parseMermaidSequence(lines []string, from int) (*graph, error) {
	m := &mermaidGraph{g: &graph{}, index: map[string]int{}}
	for i := from; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		words := strings.Fields(line)
		switch {
		case line == "" || strings.HasPrefix(line, "%%"):
		case words[0] == "autonumber" || ((words[0] == "activate" || words[0] == "deactivate") && len(words) == 2):
		case mermaidActor.MatchString(line):
			p := mermaidActor.FindStringSubmatch(line)
			m.node(p[2], mermaidLabel(p[3]), "")
		case mermaidMessage.MatchString(line):
			p := mermaidMessage.FindStringSubmatch(line)
			e := edge{from: m.node(strings.TrimSpace(p[1]), "", ""), to: m.node(p[3], "", ""), label: strings.TrimSpace(p[4]), dashed: strings.HasPrefix(p[2], "--")}
			m.g.edges = append(m.g.edges, e)
		default:
			return nil, fmt.Errorf("line %d: %q is outside the Mermaid subset kapi draws", i+1, line)
		}
	}
	if len(m.g.edges) == 0 {
		return nil, fmt.Errorf("has no messages")
	}
	return m.g, nil
}
