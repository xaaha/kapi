package render

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type vertex struct {
	real  int
	layer int
	x, w  float64
	h     float64
	extra float64
	order int
	preds []int
	succs []int
}

type chain struct {
	edge     edge
	verts    []int
	reversed bool
}

type layeredOpts struct {
	nodeGap    float64
	labelLines func(string) []string
}

type layeredResult struct {
	nodes     []rect
	conflicts int
}

func layoutLayered(s *svgWriter, sizes [][2]float64, edges []edge, opts layeredOpts) []rect {
	for attempt := 0; ; attempt++ {
		try := &svgWriter{marker: s.marker, lr: s.lr}
		res := layeredOnce(try, sizes, edges, opts, float64(attempt))
		if res.conflicts == 0 || attempt == 6 {
			s.body.Write(try.body.Bytes())
			if try.measured {
				s.extend(rect{try.minX, try.minY, try.maxX - try.minX, try.maxY - try.minY})
			}
			return res.nodes
		}
	}
}

func layeredOnce(s *svgWriter, sizes [][2]float64, edges []edge, opts layeredOpts, widen float64) layeredResult {
	n := len(sizes)
	nodeGap := opts.nodeGap + 24*widen
	reversed := backEdges(n, edges)
	var dag []chain
	var selfLoops []edge
	for i, e := range edges {
		switch {
		case e.from == e.to:
			selfLoops = append(selfLoops, e)
		case reversed[i]:
			dag = append(dag, chain{edge: e, verts: []int{e.to, e.from}, reversed: true})
		default:
			dag = append(dag, chain{edge: e, verts: []int{e.from, e.to}})
		}
	}
	labelAbs := func(text string) (cross, along float64) {
		return s.abstract(labelSize(opts.labelLines(text)))
	}
	layer := longestPath(n, dag)
	verts := make([]*vertex, n)
	for i, sz := range sizes {
		cross, along := s.abstract(sz[0], sz[1])
		verts[i] = &vertex{real: i, layer: layer[i], w: cross, h: along}
	}
	for _, e := range selfLoops {
		extra := selfLoopW + 6
		if e.label != "" {
			cross, _ := labelAbs(e.label)
			extra = selfLoopW*0.75 + 12 + cross
		}
		verts[e.from].extra = math.Max(verts[e.from].extra, extra)
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
	sep := func(a, b *vertex) float64 {
		gap := nodeGap
		if a.real < 0 || b.real < 0 {
			gap = nodeGap / 2
		}
		return a.w/2 + a.extra + gap + b.w/2
	}
	positionX(verts, layers, sep)
	gaps := make([]float64, len(layers))
	for l := range gaps {
		gaps[l] = layerGap + 40*widen
	}
	for _, c := range dag {
		if c.edge.label != "" {
			_, along := labelAbs(c.edge.label)
			l := verts[c.verts[0]].layer
			gaps[l] = math.Max(gaps[l], math.Max(labelGap, along+40)+40*widen)
		}
	}
	layerY, layerH := positionY(verts, layers, gaps)

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
		s.edgePath("kd-edge", s.curve(paths[ci]), c.edge.dashed, c.edge.twoWay)
		for k := 1; k < len(paths[ci]); k++ {
			for t := 0; t <= 24; t++ {
				samples = append(samples, sample{ci, bezierAt(paths[ci][k-1], paths[ci][k], float64(t)/24)})
			}
		}
	}
	res := layeredResult{nodes: make([]rect, n)}
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
		r, ok := placeLabel(s, a, b, ci, side, opts.labelLines(c.edge.label), labels, nodeRects, samples)
		if !ok {
			res.conflicts++
		}
		labels = append(labels, r)
	}
	for _, e := range selfLoops {
		r := nodeRects[e.from]
		x, cy := r.x+r.w, r.y+r.h/2
		loop := []point{{x, cy - 7}, {x + selfLoopW, cy - 22}, {x + selfLoopW, cy + 22}, {x + 1, cy + 7}}
		s.edgePath("kd-edge", s.cubic(loop), e.dashed, e.twoWay)
		s.extend(s.real(rect{x, cy - 22, selfLoopW, 44}))
		if e.label != "" {
			cross, _ := labelAbs(e.label)
			lr := s.labelLines(x+selfLoopW*0.75+6+cross/2, cy, opts.labelLines(e.label))
			if hitsAny(lr, labels, nodeRects) {
				res.conflicts++
			}
			labels = append(labels, lr)
		}
	}
	copy(res.nodes, nodeRects)
	return res
}

func (s *svgWriter) cubic(pts []point) string {
	p := make([]point, len(pts))
	for i, q := range pts {
		p[i] = s.realPt(q)
	}
	return fmt.Sprintf("M%s,%s C%s,%s %s,%s %s,%s", f(p[0].x), f(p[0].y), f(p[1].x), f(p[1].y), f(p[2].x), f(p[2].y), f(p[3].x), f(p[3].y))
}

func (s *svgWriter) curve(pts []point) string {
	var d strings.Builder
	at := func(p point) string {
		r := s.realPt(p)
		return f(r.x) + "," + f(r.y)
	}
	d.WriteString("M" + at(pts[0]))
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if a.x == b.x {
			d.WriteString(" L" + at(b))
			continue
		}
		mid := (a.y + b.y) / 2
		d.WriteString(" C" + at(point{a.x, mid}) + " " + at(point{b.x, mid}) + " " + at(b))
	}
	return d.String()
}

func backEdges(n int, edges []edge) map[int]bool {
	out := make([][]int, n)
	for i, e := range edges {
		out[e.from] = append(out[e.from], i)
	}
	state := make([]int, n)
	back := map[int]bool{}
	var visit func(u int)
	visit = func(u int) {
		state[u] = 1
		for _, ei := range out[u] {
			v := edges[ei].to
			switch state[v] {
			case 0:
				visit(v)
			case 1:
				back[ei] = true
			}
		}
		state[u] = 2
	}
	for u := 0; u < n; u++ {
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

type separation func(a, b *vertex) float64

func positionX(verts []*vertex, layers [][]int, sep separation) {
	for _, l := range layers {
		x := 0.0
		for i, vi := range l {
			if i > 0 {
				x += sep(verts[l[i-1]], verts[vi])
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
				placeLayer(verts, layers[l], sep, func(v *vertex) []int { return v.preds })
			}
		} else {
			for l := len(layers) - 2; l >= 0; l-- {
				placeLayer(verts, layers[l], sep, func(v *vertex) []int { return v.succs })
			}
		}
	}
}

func placeLayer(verts []*vertex, layer []int, sep separation, nbrs func(*vertex) []int) {
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
		left[i] = math.Max(left[i], left[i-1]+sep(verts[layer[i-1]], verts[layer[i]]))
	}
	right := append([]float64(nil), desired...)
	for i := len(layer) - 2; i >= 0; i-- {
		right[i] = math.Min(right[i], right[i+1]-sep(verts[layer[i]], verts[layer[i+1]]))
	}
	for i, vi := range layer {
		verts[vi].x = (left[i] + right[i]) / 2
	}
}

func positionY(verts []*vertex, layers [][]int, gaps []float64) (ys, hs []float64) {
	hs = make([]float64, len(layers))
	for _, v := range verts {
		hs[v.layer] = math.Max(hs[v.layer], v.h)
	}
	ys = make([]float64, len(layers))
	y := 0.0
	for l := range layers {
		ys[l] = y
		y += hs[l] + gaps[l]
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

const blocked = math.MaxInt - 1

func placeLabel(s *svgWriter, a, b point, chain int, prefer float64, lines []string, placed, nodes []rect, samples []sample) (rect, bool) {
	cross, along := s.abstract(labelSize(lines))
	sides := []float64{0, 1, -1}
	if prefer != 0 {
		sides = []float64{prefer, 0, -prefer}
	}
	best, bestHits := pointAtY(a, b, 0.5), math.MaxInt
	for _, frac := range []float64{0.5, 0.28, 0.72} {
		for _, side := range sides {
			p := pointAtY(a, b, frac)
			p.x += side * (cross/2 + 6)
			if hits := labelHits(rect{p.x - cross/2, p.y - along/2, cross, along}, chain, placed, nodes, samples); hits < bestHits {
				best, bestHits = p, hits
			}
		}
	}
	return s.labelLines(best.x, best.y, lines), bestHits < blocked
}

func hitsAny(r rect, placed, nodes []rect) bool {
	for _, o := range append(append([]rect(nil), placed...), nodes...) {
		if r.overlaps(o) {
			return true
		}
	}
	return false
}

func labelHits(r rect, chain int, placed, nodes []rect, samples []sample) int {
	if hitsAny(r, placed, nodes) {
		return blocked
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

func layoutFlow(g *graph, s *svgWriter) {
	sizes := make([][2]float64, len(g.names))
	lines := make([][]string, len(g.names))
	for i, name := range g.names {
		lines[i] = wrap(name)
		w := 0.0
		for _, l := range lines[i] {
			w = math.Max(w, textWidth(l))
		}
		sizes[i] = [2]float64{w + 2*nodePadX, lineH*float64(len(lines[i])) + 2*nodePadY}
	}
	opts := layeredOpts{nodeGap: nodeGap, labelLines: func(t string) []string { return []string{t} }}
	if s.lr {
		opts.nodeGap = nodeGapLR
		opts.labelLines = func(t string) []string { return wrapAtWidth(t, labelWrapAt) }
	}
	for i, r := range layoutLayered(s, sizes, g.edges, opts) {
		s.box("kd-node", r, lines[i])
	}
}
