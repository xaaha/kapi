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
	charW      = 7.4
	labelCharW = 6.6
	lineH      = 16.0
	nodePadX   = 14.0
	nodePadY   = 10.0
	nodeGap    = 60.0
	layerGap   = 52.0
	labelGap   = 84.0
	margin     = 12.0
	labelH     = 18.0
	wrapAt     = 24
	selfLoopW  = 30.0
	seqBoxH    = 34.0
	seqRowH    = 44.0
	seqSelfH   = 58.0
	seqMinGap  = 40.0
	seqBoxMinW = 84.0
)

type edge struct {
	from, to int
	label    string
	dashed   bool
}

type graph struct {
	names []string
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
			if strings.Contains(line, ":") {
				return nil, fmt.Errorf("line %d: expected `a -> b: label`, got %q", i+1, line)
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

func renderDiagram(b *bytes.Buffer, n *html.Node) error {
	kind := attrVal(n, "type")
	if kind == "" {
		kind = "flow"
	}
	if kind != "flow" && kind != "sequence" {
		return fmt.Errorf("type must be flow or sequence, not %q", kind)
	}
	src := trimBlankLines(rawText(n))
	g, err := parseGraph(src)
	if err != nil {
		return err
	}
	h := fnv.New32a()
	h.Write([]byte(attrVal(n, "data-kapi-id") + "\x00" + kind + "\x00" + src))
	svg := &svgWriter{marker: fmt.Sprintf("kapi-arrow-%08x", h.Sum32())}
	if kind == "flow" {
		layoutFlow(g, svg)
	} else {
		layoutSequence(g, svg)
	}
	writeStart(b, "figure", outerAttrs(n, "kapi-diagram kapi-diagram-"+kind))
	svg.finish(b, kind+" diagram: "+strings.Join(g.names, ", "))
	writeEnd(b, "figure")
	return nil
}

type rect struct{ x, y, w, h float64 }

func (r rect) overlaps(o rect) bool {
	return r.x < o.x+o.w && o.x < r.x+r.w && r.y < o.y+o.h && o.y < r.y+r.h
}

type svgWriter struct {
	marker   string
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

func (s *svgWriter) box(class string, r rect, lines []string) {
	s.extend(r)
	fmt.Fprintf(&s.body, `<g class="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="6"></rect>`, class, f(r.x), f(r.y), f(r.w), f(r.h))
	top := r.y + (r.h-lineH*float64(len(lines)))/2 + lineH/2
	for i, l := range lines {
		fmt.Fprintf(&s.body, `<text x="%s" y="%s">`, f(r.x+r.w/2), f(top+lineH*float64(i)))
		writeText(&s.body, l)
		s.body.WriteString("</text>")
	}
	s.body.WriteString("</g>")
}

func (s *svgWriter) path(class, d string, dashed bool) {
	if dashed {
		class += " kd-dashed"
	}
	fmt.Fprintf(&s.body, `<path class="%s" d="%s" marker-end="url(#%s)"></path>`, class, d, s.marker)
}

func (s *svgWriter) label(cx, cy float64, text string) rect {
	w := labelWidth(text)
	r := rect{cx - w/2, cy - labelH/2, w, labelH}
	s.extend(r)
	fmt.Fprintf(&s.body, `<g class="kd-label"><rect x="%s" y="%s" width="%s" height="%s" rx="4"></rect><text x="%s" y="%s">`, f(r.x), f(r.y), f(r.w), f(r.h), f(cx), f(cy))
	writeText(&s.body, text)
	s.body.WriteString("</text></g>")
	return r
}

func (s *svgWriter) finish(b *bytes.Buffer, title string) {
	x, y := s.minX-margin, s.minY-margin
	w, h := s.maxX-s.minX+2*margin, s.maxY-s.minY+2*margin
	fmt.Fprintf(b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="%s %s %s %s" width="%s" height="%s" role="img" aria-label="`, f(x), f(y), f(w), f(h), f(w), f(h))
	writeText(b, title)
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

type vertex struct {
	real  int
	layer int
	x, w  float64
	h     float64
	lines []string
	order int
	preds []int
	succs []int
}

type chain struct {
	edge     edge
	verts    []int
	reversed bool
}

func layoutFlow(g *graph, s *svgWriter) {
	n := len(g.names)
	reversed := backEdges(g)
	var dag []chain
	var selfLoops []edge
	for i, e := range g.edges {
		switch {
		case e.from == e.to:
			selfLoops = append(selfLoops, e)
		case reversed[i]:
			dag = append(dag, chain{edge: e, verts: []int{e.to, e.from}, reversed: true})
		default:
			dag = append(dag, chain{edge: e, verts: []int{e.from, e.to}})
		}
	}
	layer := longestPath(n, dag)
	verts := make([]*vertex, n)
	for i, name := range g.names {
		lines := wrap(name)
		w := 0.0
		for _, l := range lines {
			w = math.Max(w, textWidth(l))
		}
		verts[i] = &vertex{real: i, layer: layer[i], w: w + 2*nodePadX, h: lineH*float64(len(lines)) + 2*nodePadY, lines: lines}
	}
	for ci := range dag {
		c := &dag[ci]
		u, v := c.verts[0], c.verts[1]
		path := []int{u}
		for l := layer[u] + 1; l < layer[v]; l++ {
			verts = append(verts, &vertex{real: -1, layer: l, w: 0})
			path = append(path, len(verts)-1)
		}
		path = append(path, v)
		c.verts = path
		for k := 0; k+1 < len(c.verts); k++ {
			a, b := c.verts[k], c.verts[k+1]
			verts[a].succs = append(verts[a].succs, b)
			verts[b].preds = append(verts[b].preds, a)
		}
	}
	layers := orderLayers(verts)
	positionX(verts, layers)
	layerY, layerH := positionY(verts, layers, dag)

	nodeRects := make([]rect, n)
	for i := 0; i < n; i++ {
		v := verts[i]
		top := layerY[v.layer] + (layerH[v.layer]-v.h)/2
		nodeRects[i] = rect{v.x - v.w/2, top, v.w, v.h}
	}
	ports := assignPorts(verts, dag)
	paths := make([][]point, len(dag))
	var samples []sample
	for ci, c := range dag {
		paths[ci] = chainPoints(c, verts, nodeRects, layerY, layerH, ports[ci])
		s.path("kd-edge", pathData(paths[ci]), c.edge.dashed)
		for k := 1; k < len(paths[ci]); k++ {
			for t := 0; t <= 24; t++ {
				samples = append(samples, sample{ci, bezierAt(paths[ci][k-1], paths[ci][k], float64(t)/24)})
			}
		}
	}
	var labels []rect
	for ci, c := range dag {
		if c.edge.label == "" {
			continue
		}
		pts := paths[ci]
		a, b := pts[0], pts[1]
		if c.reversed {
			a, b = pts[len(pts)-1], pts[len(pts)-2]
		}
		side := math.Copysign(1, ports[ci].out-verts[c.verts[0]].x)
		if math.Abs(ports[ci].out-verts[c.verts[0]].x) < 1 {
			side = 0
		}
		labels = append(labels, placeLabel(s, a, b, ci, side, c.edge.label, labels, nodeRects, samples))
	}
	for _, e := range selfLoops {
		r := nodeRects[e.from]
		x, cy := r.x+r.w, r.y+r.h/2
		d := fmt.Sprintf("M%s,%s C%s,%s %s,%s %s,%s", f(x), f(cy-7), f(x+selfLoopW), f(cy-22), f(x+selfLoopW), f(cy+22), f(x+1), f(cy+7))
		s.path("kd-edge", d, e.dashed)
		s.extend(rect{x, cy - 22, selfLoopW, 44})
		if e.label != "" {
			s.label(x+selfLoopW*0.75+6+labelWidth(e.label)/2, cy, e.label)
		}
	}
	for i := 0; i < n; i++ {
		s.box("kd-node", nodeRects[i], verts[i].lines)
	}
}

func backEdges(g *graph) map[int]bool {
	out := make([][]int, len(g.names))
	for i, e := range g.edges {
		out[e.from] = append(out[e.from], i)
	}
	state := make([]int, len(g.names))
	back := map[int]bool{}
	var visit func(u int)
	visit = func(u int) {
		state[u] = 1
		for _, ei := range out[u] {
			v := g.edges[ei].to
			switch state[v] {
			case 0:
				visit(v)
			case 1:
				back[ei] = true
			}
		}
		state[u] = 2
	}
	for u := range g.names {
		if state[u] == 0 {
			visit(u)
		}
	}
	return back
}

func longestPath(n int, dag []chain) []int {
	indeg := make([]int, n)
	succ := make([][]int, n)
	for _, c := range dag {
		u, v := c.verts[0], c.verts[len(c.verts)-1]
		succ[u] = append(succ[u], v)
		indeg[v]++
	}
	layer := make([]int, n)
	var queue []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range succ[u] {
			layer[v] = max(layer[v], layer[u]+1)
			if indeg[v]--; indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	return layer
}

func orderLayers(verts []*vertex) [][]int {
	depth := 0
	for _, v := range verts {
		depth = max(depth, v.layer+1)
	}
	layers := make([][]int, depth)
	for i, v := range verts {
		layers[v.layer] = append(layers[v.layer], i)
	}
	setOrder := func() {
		for _, l := range layers {
			for pos, vi := range l {
				verts[vi].order = pos
			}
		}
	}
	setOrder()
	best := cloneLayers(layers)
	bestCross := crossings(verts, layers)
	for iter := 0; iter < 8 && bestCross > 0; iter++ {
		if iter%2 == 0 {
			for l := 1; l < depth; l++ {
				sortByBarycenter(verts, layers[l], func(v *vertex) []int { return v.preds })
				setOrder()
			}
		} else {
			for l := depth - 2; l >= 0; l-- {
				sortByBarycenter(verts, layers[l], func(v *vertex) []int { return v.succs })
				setOrder()
			}
		}
		if c := crossings(verts, layers); c < bestCross {
			best, bestCross = cloneLayers(layers), c
		}
	}
	for _, l := range best {
		for pos, vi := range l {
			verts[vi].order = pos
		}
	}
	return best
}

func cloneLayers(layers [][]int) [][]int {
	out := make([][]int, len(layers))
	for i, l := range layers {
		out[i] = append([]int(nil), l...)
	}
	return out
}

func sortByBarycenter(verts []*vertex, layer []int, nbrs func(*vertex) []int) {
	bary := make(map[int]float64, len(layer))
	for _, vi := range layer {
		ns := nbrs(verts[vi])
		if len(ns) == 0 {
			bary[vi] = float64(verts[vi].order)
			continue
		}
		sum := 0.0
		for _, u := range ns {
			sum += float64(verts[u].order)
		}
		bary[vi] = sum / float64(len(ns))
	}
	sort.SliceStable(layer, func(i, j int) bool { return bary[layer[i]] < bary[layer[j]] })
}

func crossings(verts []*vertex, layers [][]int) int {
	total := 0
	for _, l := range layers {
		var segs [][2]int
		for _, vi := range l {
			for _, s := range verts[vi].succs {
				segs = append(segs, [2]int{verts[vi].order, verts[s].order})
			}
		}
		for i := range segs {
			for j := i + 1; j < len(segs); j++ {
				if (segs[i][0]-segs[j][0])*(segs[i][1]-segs[j][1]) < 0 {
					total++
				}
			}
		}
	}
	return total
}

func separation(a, b *vertex) float64 {
	gap := nodeGap
	if a.real < 0 || b.real < 0 {
		gap = nodeGap / 2
	}
	return a.w/2 + gap + b.w/2
}

func positionX(verts []*vertex, layers [][]int) {
	for _, l := range layers {
		x := 0.0
		for i, vi := range l {
			if i > 0 {
				x += separation(verts[l[i-1]], verts[vi])
			}
			verts[vi].x = x
		}
		shift := x / 2
		for _, vi := range l {
			verts[vi].x -= shift
		}
	}
	for iter := 0; iter < 6; iter++ {
		if iter%2 == 0 {
			for l := 1; l < len(layers); l++ {
				placeLayer(verts, layers[l], func(v *vertex) []int { return v.preds })
			}
		} else {
			for l := len(layers) - 2; l >= 0; l-- {
				placeLayer(verts, layers[l], func(v *vertex) []int { return v.succs })
			}
		}
	}
}

func placeLayer(verts []*vertex, layer []int, nbrs func(*vertex) []int) {
	desired := make([]float64, len(layer))
	for i, vi := range layer {
		v := verts[vi]
		ns := nbrs(v)
		if len(ns) == 0 {
			desired[i] = v.x
			continue
		}
		sum := 0.0
		for _, u := range ns {
			sum += verts[u].x
		}
		desired[i] = sum / float64(len(ns))
	}
	left := append([]float64(nil), desired...)
	for i := 1; i < len(layer); i++ {
		left[i] = math.Max(left[i], left[i-1]+separation(verts[layer[i-1]], verts[layer[i]]))
	}
	right := append([]float64(nil), desired...)
	for i := len(layer) - 2; i >= 0; i-- {
		right[i] = math.Min(right[i], right[i+1]-separation(verts[layer[i]], verts[layer[i+1]]))
	}
	for i, vi := range layer {
		verts[vi].x = (left[i] + right[i]) / 2
	}
}

func positionY(verts []*vertex, layers [][]int, dag []chain) (ys, hs []float64) {
	hs = make([]float64, len(layers))
	for _, v := range verts {
		hs[v.layer] = math.Max(hs[v.layer], v.h)
	}
	labelled := make([]bool, len(layers))
	for _, c := range dag {
		if c.edge.label != "" {
			labelled[verts[c.verts[0]].layer] = true
		}
	}
	ys = make([]float64, len(layers))
	y := 0.0
	for l := range layers {
		ys[l] = y
		gap := layerGap
		if labelled[l] {
			gap = labelGap
		}
		y += hs[l] + gap
	}
	return ys, hs
}

type ports struct{ out, in float64 }

type portEnd struct {
	chain int
	other float64
}

func assignPorts(verts []*vertex, dag []chain) []ports {
	res := make([]ports, len(dag))
	outs := make([][]portEnd, len(verts))
	ins := make([][]portEnd, len(verts))
	for ci, c := range dag {
		u, v := c.verts[0], c.verts[len(c.verts)-1]
		outs[u] = append(outs[u], portEnd{ci, verts[c.verts[1]].x})
		ins[v] = append(ins[v], portEnd{ci, verts[c.verts[len(c.verts)-2]].x})
	}
	for vi, v := range verts {
		for ci, x := range spread(v, outs[vi]) {
			res[ci].out = x
		}
		for ci, x := range spread(v, ins[vi]) {
			res[ci].in = x
		}
	}
	return res
}

func spread(v *vertex, ends []portEnd) map[int]float64 {
	out := make(map[int]float64, len(ends))
	sort.SliceStable(ends, func(i, j int) bool { return ends[i].other < ends[j].other })
	step := math.Min(16, (v.w-16)/float64(max(len(ends), 1)))
	for i, e := range ends {
		out[e.chain] = v.x + (float64(i)-float64(len(ends)-1)/2)*step
	}
	return out
}

type sample struct {
	chain int
	p     point
}

type point struct{ x, y float64 }

func chainPoints(c chain, verts []*vertex, nodes []rect, layerY, layerH []float64, port ports) []point {
	var pts []point
	for k, vi := range c.verts {
		v := verts[vi]
		switch {
		case k == 0:
			r := nodes[v.real]
			pts = append(pts, point{port.out, r.y + r.h})
		case k == len(c.verts)-1:
			r := nodes[v.real]
			pts = append(pts, point{port.in, r.y})
		default:
			pts = append(pts, point{v.x, layerY[v.layer]}, point{v.x, layerY[v.layer] + layerH[v.layer]})
		}
	}
	if c.reversed {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	return pts
}

func pathData(pts []point) string {
	var d strings.Builder
	d.WriteString("M" + f(pts[0].x) + "," + f(pts[0].y))
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if a.x == b.x {
			d.WriteString(" L" + f(b.x) + "," + f(b.y))
			continue
		}
		mid := (a.y + b.y) / 2
		d.WriteString(" C" + f(a.x) + "," + f(mid) + " " + f(b.x) + "," + f(mid) + " " + f(b.x) + "," + f(b.y))
	}
	return d.String()
}

func bezierAt(a, b point, t float64) point {
	mid := (a.y + b.y) / 2
	u := 1 - t
	x := u*u*u*a.x + 3*u*u*t*a.x + 3*u*t*t*b.x + t*t*t*b.x
	y := u*u*u*a.y + 3*u*u*t*mid + 3*u*t*t*mid + t*t*t*b.y
	return point{x, y}
}

func pointAtY(a, b point, frac float64) point {
	target := a.y + frac*(b.y-a.y)
	lo, hi := 0.0, 1.0
	for range 40 {
		mid := (lo + hi) / 2
		if (bezierAt(a, b, mid).y < target) == (a.y < b.y) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return bezierAt(a, b, (lo+hi)/2)
}

func placeLabel(s *svgWriter, a, b point, chain int, prefer float64, text string, placed, nodes []rect, samples []sample) rect {
	w := labelWidth(text)
	sides := []float64{0, 1, -1}
	if prefer != 0 {
		sides = []float64{prefer, 0, -prefer}
	}
	best, bestHits := pointAtY(a, b, 0.5), math.MaxInt
	for _, frac := range []float64{0.5, 0.28, 0.72} {
		for _, side := range sides {
			p := pointAtY(a, b, frac)
			p.x += side * (w/2 + 6)
			if hits := labelHits(rect{p.x - w/2, p.y - labelH/2, w, labelH}, chain, placed, nodes, samples); hits < bestHits {
				best, bestHits = p, hits
			}
		}
	}
	return s.label(best.x, best.y, text)
}

func labelHits(r rect, chain int, placed, nodes []rect, samples []sample) int {
	for _, o := range placed {
		if r.overlaps(o) {
			return math.MaxInt - 1
		}
	}
	for _, o := range nodes {
		if r.overlaps(o) {
			return math.MaxInt - 1
		}
	}
	hits := 0
	pad := rect{r.x - 2, r.y - 2, r.w + 4, r.h + 4}
	for _, sm := range samples {
		if sm.chain != chain && pad.overlaps(rect{sm.p.x, sm.p.y, 0.01, 0.01}) {
			hits++
		}
	}
	return hits
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
		s.box("kd-node kd-actor", rect{xs[i] - widths[i]/2, 0, widths[i], seqBoxH}, []string{name})
	}
}

func span(e edge) int {
	if e.from > e.to {
		return e.from - e.to
	}
	return e.to - e.from
}
