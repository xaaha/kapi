package render

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	archTitleCharW = 7.6
	archSubCharW   = 6.5
	archTitleH     = 17.0
	archSubH       = 15.0
	archPadX       = 14.0
	archPadY       = 12.0
	archSubGap     = 5.0
	archMinW       = 110.0
	archTitleWrap  = 34
	archSubWrap    = 40
	archGapLR      = 40.0
	archGapTB      = 56.0
)

type archNode struct {
	title  []string
	subs   []string
	state  string
	tone   string
	dashed bool
}

type archGraph struct {
	nodes []archNode
	edges []edge
}

var archStates = map[string]string{"built": "accent", "review": "warning", "planned": "neutral"}

var legendStates = []struct{ state, label string }{{"built", "built"}, {"review", "in review"}, {"planned", "planned"}}

var archID = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func findArrow(s string) (at int, arrow string) {
	for _, a := range []string{"<->", "-->", "->"} {
		if i := strings.Index(s, a); i >= 0 {
			return i, a
		}
	}
	return -1, ""
}

func parseArchitecture(src string) (*archGraph, error) {
	g := &archGraph{}
	index := map[string]int{}
	type ref struct {
		line     int
		from, to string
		e        edge
	}
	var refs []ref
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "%%") {
			continue
		}
		head, rest, hasColon := strings.Cut(line, ":")
		at, arrow := findArrow(head)
		if at >= 0 {
			from, to := strings.TrimSpace(head[:at]), strings.TrimSpace(head[at+len(arrow):])
			for _, id := range []string{from, to} {
				if !archID.MatchString(id) {
					return nil, fmt.Errorf("line %d: expected `a -> b: label` with ids of letters, digits, - _ or ., got %q", i+1, line)
				}
			}
			e := edge{dashed: arrow == "-->", twoWay: arrow == "<->"}
			if hasColon {
				e.label = strings.TrimSpace(rest)
			}
			refs = append(refs, ref{i + 1, from, to, e})
			continue
		}
		if !hasColon {
			return nil, fmt.Errorf("line %d: expected `id: Title | subtitle | state` or `a -> b: label`, got %q", i+1, line)
		}
		id := strings.TrimSpace(head)
		if !archID.MatchString(id) {
			return nil, fmt.Errorf("line %d: node id %q may hold only letters, digits, - _ and .", i+1, id)
		}
		if _, dup := index[id]; dup {
			return nil, fmt.Errorf("line %d: node %q is declared twice", i+1, id)
		}
		var parts []string
		for _, p := range strings.Split(rest, "|") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("line %d: node %q needs a title after the colon", i+1, id)
		}
		node := archNode{title: wrapAtWidth(parts[0], archTitleWrap), tone: "neutral"}
		subs := parts[1:]
		if last := len(subs) - 1; last >= 0 {
			if tone, ok := archStates[subs[last]]; ok {
				node.state, node.tone, subs = subs[last], tone, subs[:last]
			} else if isTone(subs[last]) {
				node.tone, subs = subs[last], subs[:last]
			}
		}
		node.dashed = node.state == "planned"
		for _, sub := range subs {
			node.subs = append(node.subs, wrapAtWidth(sub, archSubWrap)...)
		}
		index[id] = len(g.nodes)
		g.nodes = append(g.nodes, node)
	}
	if len(g.nodes) == 0 {
		return nil, fmt.Errorf("has no nodes; write one per line as `id: Title | subtitle | state`")
	}
	for _, r := range refs {
		for _, id := range []string{r.from, r.to} {
			if _, ok := index[id]; !ok {
				return nil, fmt.Errorf("line %d: no node %q; declare it on its own line as `%s: Title`", r.line, id, id)
			}
		}
		r.e.from, r.e.to = index[r.from], index[r.to]
		g.edges = append(g.edges, r.e)
	}
	return g, nil
}

func (g *archGraph) legend() []string {
	used := map[string]bool{}
	for _, n := range g.nodes {
		used[n.state] = true
	}
	var out []string
	for _, s := range legendStates {
		if used[s.state] {
			out = append(out, s.state)
		}
	}
	return out
}

func writeLegend(b *bytes.Buffer, states []string) {
	if len(states) == 0 {
		return
	}
	b.WriteString(`<figcaption class="kapi-diagram-legend">`)
	for i, st := range states {
		if i > 0 {
			b.WriteByte(' ')
		}
		for _, s := range legendStates {
			if s.state != st {
				continue
			}
			class := "kapi-badge kapi-tone-" + archStates[st]
			if st == "planned" {
				class += " kd-legend-planned"
			}
			b.WriteString(`<span class="` + class + `">` + s.label + "</span>")
		}
	}
	b.WriteString("</figcaption>")
}

func (n archNode) size() (w, h float64) {
	for _, l := range n.title {
		w = math.Max(w, float64(len([]rune(l)))*archTitleCharW)
	}
	for _, l := range n.subs {
		w = math.Max(w, float64(len([]rune(l)))*archSubCharW)
	}
	h = 2*archPadY + archTitleH*float64(len(n.title))
	if len(n.subs) > 0 {
		h += archSubGap + archSubH*float64(len(n.subs))
	}
	return math.Max(archMinW, w+2*archPadX), h
}

func layoutArchitecture(g *archGraph, s *svgWriter) {
	sizes := make([][2]float64, len(g.nodes))
	for i, n := range g.nodes {
		w, h := n.size()
		sizes[i] = [2]float64{w, h}
	}
	opts := layeredOpts{nodeGap: archGapTB, flatTwoWay: true, labelLines: func(t string) []string { return wrapAtWidth(t, labelWrapAt) }}
	if s.lr {
		opts.nodeGap = archGapLR
	}
	for i, at := range layoutLayered(s, sizes, g.edges, opts) {
		s.archBox(g.nodes[i], at)
	}
}

func (s *svgWriter) archBox(n archNode, at rect) {
	r := s.real(at)
	s.extend(r)
	class := "kd-node kd-arch kapi-tone-" + n.tone
	if n.dashed {
		class += " kd-planned"
	}
	fmt.Fprintf(&s.body, `<g class="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="10"></rect>`, class, f(r.x), f(r.y), f(r.w), f(r.h))
	x, y := r.x+archPadX, r.y+archPadY
	title := "kd-title"
	if len(n.subs) == 0 {
		x, title = r.x+r.w/2, "kd-title kd-centred"
	}
	for _, l := range n.title {
		fmt.Fprintf(&s.body, `<text class="%s" x="%s" y="%s">`, title, f(x), f(y+archTitleH/2))
		writeText(&s.body, l)
		s.body.WriteString("</text>")
		y += archTitleH
	}
	y += archSubGap
	for _, l := range n.subs {
		fmt.Fprintf(&s.body, `<text class="kd-sub" x="%s" y="%s">`, f(x), f(y+archSubH/2))
		writeText(&s.body, l)
		s.body.WriteString("</text>")
		y += archSubH
	}
	s.body.WriteString("</g>")
}
