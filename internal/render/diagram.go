package render

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	charW       = 7.4
	labelCharW  = 6.6
	lineH       = 16.0
	nodePadX    = 14.0
	nodePadY    = 10.0
	nodeGap     = 60.0
	nodeGapLR   = 36.0
	labelWrapAt = 22
	wideAt      = 720.0
	layerGap    = 52.0
	labelGap    = 84.0
	margin      = 12.0
	labelH      = 18.0
	labelLineH  = 14.0
	wrapAt      = 24
	selfLoopW   = 30.0
	seqBoxH     = 34.0
	seqRowH     = 44.0
	seqSelfH    = 58.0
	seqMinGap   = 40.0
	seqBoxMinW  = 84.0
	kindIconW   = 44.0
	kindIconGap = 6.0
	kindMinW    = 104.0
	stateIconW  = 16.0
	stateInset  = 7.0
)

type nodeLook struct {
	kind   string
	state  string
	dashed bool
}

var ignoredTags = set("built", "review")

func parseTags(s string) (nodeLook, bool) {
	var look nodeLook
	words := strings.Fields(s)
	for _, w := range words {
		switch {
		case w == "planned":
			look.dashed = true
		case kindIcons[w] != "":
			look.kind = w
		case statusIcons[w].tone != "":
			look.state = w
		case ignoredTags[w], isTone(w):
		default:
			return nodeLook{}, false
		}
	}
	return look, len(words) > 0
}

func lenientTags(s string) nodeLook {
	var look nodeLook
	for _, w := range strings.Fields(s) {
		if one, ok := parseTags(w); ok {
			look.dashed = look.dashed || one.dashed
			if one.kind != "" {
				look.kind = one.kind
			}
			if one.state != "" {
				look.state = one.state
			}
		}
	}
	return look
}

func (l nodeLook) grow(w, h float64) (float64, float64) {
	if l.kind != "" {
		return math.Max(w, kindMinW), h + kindIconW + kindIconGap
	}
	if l.state != "" {
		w += stateIconW + stateInset
	}
	return w, h
}

func (s *svgWriter) openNode(class string, r rect, l nodeLook) {
	if l.dashed {
		class += " kd-planned"
	}
	if l.state != "" {
		class += " kd-stated kapi-tone-" + statusIcons[l.state].tone
	}
	fmt.Fprintf(&s.body, `<g class="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="10"></rect>`, class, f(r.x), f(r.y), f(r.w), f(r.h))
}

func (s *svgWriter) nodeIcons(r rect, l nodeLook, padY float64) (textTop float64) {
	textTop = r.y + padY
	if l.kind != "" {
		fmt.Fprintf(&s.body, `<svg class="kd-icon kd-kind-%s" x="%s" y="%s" width="%s" height="%s" viewBox="0 0 256 256">%s</svg>`,
			l.kind, f(r.x+(r.w-kindIconW)/2), f(textTop), f(kindIconW), f(kindIconW), kindIcons[l.kind])
		textTop += kindIconW + kindIconGap
	}
	if l.state != "" {
		x, y := r.x+r.w-stateIconW-stateInset, r.y+stateInset
		if l.kind == "" {
			x, y = r.x+stateInset+2, r.y+(r.h-stateIconW)/2
		}
		fmt.Fprintf(&s.body, `<svg class="kd-state" x="%s" y="%s" width="%s" height="%s" viewBox="0 0 256 256" role="img" aria-label="%s">%s</svg>`,
			f(x), f(y), f(stateIconW), f(stateIconW), statusIcons[l.state].label, statusIcons[l.state].paths)
	}
	return textTop
}

type edge struct {
	from, to int
	label    string
	dashed   bool
	twoWay   bool
}

type graph struct {
	names []string
	looks []nodeLook
	edges []edge
}

func parseGraph(src string) (*graph, error) {
	g := &graph{}
	index := map[string]int{}
	node := func(name string) int {
		if i, ok := index[name]; ok {
			return i
		}
		index[name] = len(g.names)
		g.names = append(g.names, name)
		g.looks = append(g.looks, nodeLook{})
		return len(g.names) - 1
	}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "%%") {
			continue
		}
		arrow, dashed := "->", false
		at := strings.Index(line, "-->")
		if at >= 0 {
			arrow, dashed = "-->", true
		} else {
			at = strings.Index(line, "->")
		}
		if at < 0 {
			if name, tags, ok := strings.Cut(line, ":"); ok {
				if name = strings.TrimSpace(name); name == "" {
					return nil, fmt.Errorf("line %d: expected `a -> b: label` or `node: kind state`, got %q", i+1, line)
				}
				g.looks[node(name)] = lenientTags(tags)
				continue
			}
			node(line)
			continue
		}
		from := strings.TrimSpace(line[:at])
		rest := line[at+len(arrow):]
		to, label := rest, ""
		if c := strings.IndexByte(rest, ':'); c >= 0 {
			to, label = rest[:c], strings.TrimSpace(rest[c+1:])
		}
		to = strings.TrimSpace(to)
		if from == "" || to == "" {
			return nil, fmt.Errorf("line %d: expected `a -> b: label`, got %q", i+1, line)
		}
		g.edges = append(g.edges, edge{from: node(from), to: node(to), label: label, dashed: dashed})
	}
	if len(g.names) == 0 {
		return nil, fmt.Errorf("has no edges; write one per line as `a -> b: label`")
	}
	return g, nil
}

var defaultDir = map[string]string{"flow": "tb", "sequence": "", "architecture": "lr"}

func renderDiagram(b *bytes.Buffer, n *html.Node) error {
	kind := attrVal(n, "type")
	if kind == "" {
		kind = "flow"
	}
	dir, ok := defaultDir[kind]
	if !ok {
		return fmt.Errorf("type must be flow, sequence or architecture, not %q", kind)
	}
	if given := attrVal(n, "dir"); given != "" && dir != "" {
		if given != "tb" && given != "lr" {
			return fmt.Errorf("dir must be tb (top to bottom) or lr (left to right), not %q", given)
		}
		dir = given
	}
	src := trimBlankLines(rawText(n))
	h := fnv.New32a()
	h.Write([]byte(attrVal(n, "data-kapi-id") + "\x00" + kind + "\x00" + src))
	svg := &svgWriter{marker: fmt.Sprintf("kapi-arrow-%08x", h.Sum32()), lr: dir == "lr"}
	var names []string
	var legend []nodeLook
	if kind == "architecture" {
		a, err := parseArchitecture(src)
		if err != nil {
			return err
		}
		layoutArchitecture(a, svg)
		for _, node := range a.nodes {
			names = append(names, strings.Join(node.title, " "))
		}
		legend = a.legend()
	} else {
		g, err := parseGraph(src)
		if err != nil {
			return err
		}
		if kind == "flow" {
			layoutFlow(g, svg)
			legend = g.looks
		} else {
			layoutSequence(g, svg)
		}
		names = g.names
	}
	var extra []string
	if dir != "" {
		extra = []string{"data-kapi-dir", dir}
	}
	class := "kapi-diagram kapi-diagram-" + kind
	if kind == "architecture" && svg.maxX-svg.minX+2*margin > wideAt {
		class += " kapi-diagram-wide"
	}
	writeStart(b, "figure", outerAttrs(n, class, extra...))
	svg.finish(b, kind+" diagram: "+strings.Join(names, ", "))
	writeLegend(b, legend)
	writeEnd(b, "figure")
	return nil
}

type rect struct{ x, y, w, h float64 }

func (r rect) overlaps(o rect) bool {
	return r.x < o.x+o.w && o.x < r.x+r.w && r.y < o.y+o.h && o.y < r.y+r.h
}

type svgWriter struct {
	marker   string
	lr       bool
	body     bytes.Buffer
	minX     float64
	minY     float64
	maxX     float64
	maxY     float64
	measured bool
}

func (s *svgWriter) extend(r rect) {
	if !s.measured {
		s.minX, s.minY, s.maxX, s.maxY, s.measured = r.x, r.y, r.x+r.w, r.y+r.h, true
		return
	}
	s.minX = math.Min(s.minX, r.x)
	s.minY = math.Min(s.minY, r.y)
	s.maxX = math.Max(s.maxX, r.x+r.w)
	s.maxY = math.Max(s.maxY, r.y+r.h)
}

func f(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

func (s *svgWriter) real(r rect) rect {
	if s.lr {
		return rect{r.y, r.x, r.h, r.w}
	}
	return r
}

func (s *svgWriter) realPt(p point) point {
	if s.lr {
		return point{p.y, p.x}
	}
	return p
}

func (s *svgWriter) abstract(w, h float64) (cross, along float64) {
	if s.lr {
		return h, w
	}
	return w, h
}

func (s *svgWriter) box(class string, at rect, lines []string, look nodeLook) {
	r := s.real(at)
	s.extend(r)
	s.openNode(class, r, look)
	textTop := s.nodeIcons(r, look, nodePadY)
	cx := r.x + r.w/2
	top := r.y + (r.h-lineH*float64(len(lines)))/2 + lineH/2
	switch {
	case look.kind != "":
		top = textTop + lineH/2
	case look.state != "":
		cx += (stateIconW + stateInset) / 2
	}
	for i, l := range lines {
		fmt.Fprintf(&s.body, `<text x="%s" y="%s">`, f(cx), f(top+lineH*float64(i)))
		writeText(&s.body, l)
		s.body.WriteString("</text>")
	}
	s.body.WriteString("</g>")
}

func (s *svgWriter) path(class, d string, dashed bool) {
	s.edgePath(class, d, dashed, false)
}

func (s *svgWriter) edgePath(class, d string, dashed, twoWay bool) {
	if dashed {
		class += " kd-dashed"
	}
	start := ""
	if twoWay {
		start = ` marker-start="url(#` + s.marker + `)"`
	}
	fmt.Fprintf(&s.body, `<path class="%s" d="%s"%s marker-end="url(#%s)"></path>`, class, d, start, s.marker)
}

func (s *svgWriter) label(cx, cy float64, text string) rect {
	return s.labelLines(cx, cy, []string{text})
}

func labelSize(lines []string) (w, h float64) {
	for _, l := range lines {
		w = math.Max(w, labelWidth(l))
	}
	return w, labelH + float64(len(lines)-1)*labelLineH
}

func (s *svgWriter) labelLines(cx, cy float64, lines []string) rect {
	cross, along := s.abstract(labelSize(lines))
	at := rect{cx - cross/2, cy - along/2, cross, along}
	r := s.real(at)
	s.extend(r)
	fmt.Fprintf(&s.body, `<g class="kd-label"><rect x="%s" y="%s" width="%s" height="%s" rx="4"></rect>`, f(r.x), f(r.y), f(r.w), f(r.h))
	for i, l := range lines {
		fmt.Fprintf(&s.body, `<text x="%s" y="%s">`, f(r.x+r.w/2), f(r.y+labelH/2+labelLineH*float64(i)))
		writeText(&s.body, l)
		s.body.WriteString("</text>")
	}
	s.body.WriteString("</g>")
	return at
}

func (s *svgWriter) finish(b *bytes.Buffer, title string) {
	x, y := s.minX-margin, s.minY-margin
	w, h := s.maxX-s.minX+2*margin, s.maxY-s.minY+2*margin
	fmt.Fprintf(b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="%s %s %s %s" width="%s" height="%s" role="img" aria-label="`, f(x), f(y), f(w), f(h), f(w), f(h))
	b.WriteString(attrEscaper.Replace(title))
	b.WriteString(`"><title>`)
	writeText(b, title)
	fmt.Fprintf(b, `</title><defs><marker id="%s" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path class="kd-arrow" d="M0,0 L10,5 L0,10 z"></path></marker></defs>`, s.marker)
	b.Write(s.body.Bytes())
	b.WriteString("</svg>")
}

func textWidth(s string) float64 {
	return float64(len([]rune(s))) * charW
}

func labelWidth(s string) float64 {
	return float64(len([]rune(s)))*labelCharW + 10
}

func wrap(s string) []string {
	return wrapAtWidth(s, wrapAt)
}

func wrapAtWidth(s string, wrapAt int) []string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		if cur != "" && len([]rune(cur))+1+len([]rune(w)) > wrapAt {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur != "" {
			cur += " "
		}
		cur += w
	}
	return append(lines, cur)
}

func layoutSequence(g *graph, s *svgWriter) {
	n := len(g.names)
	widths := make([]float64, n)
	for i, name := range g.names {
		widths[i] = math.Max(seqBoxMinW, textWidth(name)+2*nodePadX)
	}
	gaps := make([]float64, max(n-1, 0))
	for i := range gaps {
		gaps[i] = (widths[i]+widths[i+1])/2 + seqMinGap
	}
	rightPad := 0.0
	msgs := append([]edge(nil), g.edges...)
	sort.SliceStable(msgs, func(i, j int) bool { return span(msgs[i]) < span(msgs[j]) })
	for _, m := range msgs {
		need := labelWidth(m.label) + 14
		lo, hi := min(m.from, m.to), max(m.from, m.to)
		if lo == hi {
			need += selfLoopW + 8
			if lo == n-1 {
				rightPad = math.Max(rightPad, need-widths[lo]/2)
				continue
			}
			hi = lo + 1
			need += widths[hi] / 2
		}
		have := 0.0
		for i := lo; i < hi; i++ {
			have += gaps[i]
		}
		if have < need {
			extra := (need - have) / float64(hi-lo)
			for i := lo; i < hi; i++ {
				gaps[i] += extra
			}
		}
	}
	xs := make([]float64, n)
	for i := 1; i < n; i++ {
		xs[i] = xs[i-1] + gaps[i-1]
	}
	y := seqBoxH + 34
	type row struct {
		m edge
		y float64
	}
	rows := make([]row, len(g.edges))
	for i, m := range g.edges {
		rows[i] = row{m, y}
		if m.from == m.to {
			y += seqSelfH
		} else {
			y += seqRowH
		}
	}
	bottom := y - seqRowH/2 + 8
	for i := range g.names {
		fmt.Fprintf(&s.body, `<path class="kd-lifeline" d="M%s,%s L%s,%s"></path>`, f(xs[i]), f(seqBoxH), f(xs[i]), f(bottom))
	}
	s.extend(rect{xs[n-1], bottom, rightPad, 1})
	for _, r := range rows {
		a, b := xs[r.m.from], xs[r.m.to]
		if r.m.from == r.m.to {
			d := fmt.Sprintf("M%s,%s C%s,%s %s,%s %s,%s", f(a), f(r.y), f(a+selfLoopW*1.6), f(r.y-4), f(a+selfLoopW*1.6), f(r.y+22), f(a+2), f(r.y+18))
			s.path("kd-msg", d, r.m.dashed)
			if r.m.label != "" {
				s.label(a+selfLoopW*1.2+8+labelWidth(r.m.label)/2, r.y+9, r.m.label)
			}
			continue
		}
		end := b - 2
		if b < a {
			end = b + 2
		}
		s.path("kd-msg", fmt.Sprintf("M%s,%s L%s,%s", f(a), f(r.y), f(end), f(r.y)), r.m.dashed)
		if r.m.label != "" {
			s.label((a+b)/2, r.y-12, r.m.label)
		}
	}
	for i, name := range g.names {
		s.box("kd-node kd-actor", rect{xs[i] - widths[i]/2, 0, widths[i], seqBoxH}, []string{name}, nodeLook{})
	}
}

func span(e edge) int {
	if e.from > e.to {
		return e.from - e.to
	}
	return e.to - e.from
}
