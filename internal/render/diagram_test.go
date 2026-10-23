package render

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func flowSources() map[string]string {
	srcs := map[string]string{
		"chain":   "a -> b\nb -> c\nc -> d\nd -> e",
		"diamond": "start -> left: yes\nstart -> right: no\nleft -> end: merge\nright -> end: merge",
		"cycle":   "plan -> build: go\nbuild -> test: run\ntest -> plan: fails\ntest -> ship: passes",
		"fanout":  "hub -> one: a\nhub -> two: b\nhub -> three: c\nhub -> four: d\nhub -> five: e",
		"bidirectional": "page file -> daemon: mtime change\ndaemon -> page file: applies\n" +
			"daemon -> browser: revision event\nbrowser -> daemon: op\ndaemon -> event log: appends",
		"parallel":   "a -> b: first long label\na -> b: second long label\na -> b: third long label",
		"self loops": "a -> b\na -> c\nb -> b: retries with a long label\nc -> c: also loops\nb -> d\nc -> d",
		"long edge":  "a -> b\nb -> c\nc -> d\na -> d: skips two layers\nd -> a: loops back up",
	}
	seed := uint32(7)
	pick := func(n int) int {
		seed = seed*1103515245 + 12345
		return int(seed>>16) % n
	}
	for g := 0; g < 12; g++ {
		n := 6 + g%10
		var lines []string
		for i := 1; i < n; i++ {
			lines = append(lines, fmt.Sprintf("node %d -> node %d: e%d", pick(i), i, i))
		}
		for k := 0; k < n/3; k++ {
			a, b := pick(n), pick(n)
			if a != b {
				lines = append(lines, fmt.Sprintf("node %d -> node %d", a, b))
			}
		}
		srcs[fmt.Sprintf("random %d", g)] = strings.Join(lines, "\n")
	}
	return srcs
}

func diagram(kind, body string) string {
	return diagramDir(kind, "", body)
}

func diagramDir(kind, dir, body string) string {
	if dir != "" {
		dir = ` dir="` + dir + `"`
	}
	return `<kapi-diagram type="` + kind + `"` + dir + ` data-kapi-id="dddddddd">` + "\n" + body + "\n</kapi-diagram>"
}

var dirs = []string{"tb", "lr"}

func TestDiagramLayoutIsDeterministic(t *testing.T) {
	for name, body := range flowSources() {
		for _, kind := range []string{"flow", "sequence"} {
			for _, dir := range dirs {
				src := diagramDir(kind, dir, body)
				first := render(t, src)
				for i := 0; i < 20; i++ {
					if got := render(t, src); got != first {
						t.Fatalf("%s %s %s: render %d differs from the first", kind, dir, name, i)
					}
				}
			}
		}
	}
}

type box struct {
	class string
	r     rect
	text  string
}

func diagramBoxes(t *testing.T, out string) []box {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	var boxes []box
	var walk func(n *html.Node, class string)
	walk = func(n *html.Node, class string) {
		if n.Type == html.ElementNode && n.Data == "g" {
			class = attrVal(n, "class")
		}
		if n.Type == html.ElementNode && n.Data == "rect" && class != "" {
			num := func(k string) float64 {
				v, err := strconv.ParseFloat(attrVal(n, k), 64)
				if err != nil {
					t.Fatalf("rect %s=%q: %v", k, attrVal(n, k), err)
				}
				return v
			}
			var text bytes.Buffer
			for s := n.NextSibling; s != nil; s = s.NextSibling {
				if s.Data == "text" && s.FirstChild != nil {
					text.WriteString(s.FirstChild.Data + " ")
				}
			}
			boxes = append(boxes, box{class, rect{num("x"), num("y"), num("width"), num("height")}, strings.TrimSpace(text.String())})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, class)
		}
	}
	walk(doc, "")
	return boxes
}

func TestFlowLayoutHasNoOverlaps(t *testing.T) {
	for name, body := range flowSources() {
		for _, dir := range dirs {
			out := render(t, diagramDir("flow", dir, body))
			if strings.Contains(out, "kapi-error") {
				t.Fatalf("%s %s: %s", dir, name, out)
			}
			assertNoOverlaps(t, dir+" "+name, diagramBoxes(t, out))
		}
	}
}

func assertNoOverlaps(t *testing.T, name string, boxes []box) {
	t.Helper()
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			if boxes[i].r.overlaps(boxes[j].r) {
				t.Errorf("%s: %s %q overlaps %s %q", name, boxes[i].class, boxes[i].text, boxes[j].class, boxes[j].text)
			}
		}
	}
}

func TestFlowLeftToRightPointsRight(t *testing.T) {
	out := render(t, diagramDir("flow", "lr", "a -> b\nb -> c\na -> c"))
	x := map[string]float64{}
	for _, b := range diagramBoxes(t, out) {
		x[b.text] = b.r.x
	}
	if x["a"] >= x["b"] || x["b"] >= x["c"] {
		t.Errorf("lr flow layers are not left to right: %v", x)
	}
	if !strings.Contains(out, `data-kapi-dir="lr"`) {
		t.Errorf("rendered diagram does not say its direction: %s", out)
	}
	if tb := render(t, diagram("flow", "a -> b")); !strings.Contains(tb, `data-kapi-dir="tb"`) {
		t.Errorf("flow should default to tb: %s", tb)
	}
}

func TestSequenceIgnoresDir(t *testing.T) {
	plain := render(t, diagram("sequence", "a -> b: hi"))
	for _, dir := range []string{"lr", "tb", "sideways"} {
		if got := render(t, diagramDir("sequence", dir, "a -> b: hi")); got != plain {
			t.Errorf("dir=%s changed a sequence diagram:\n%s\n%s", dir, got, plain)
		}
	}
	if strings.Contains(plain, "data-kapi-dir") {
		t.Errorf("a sequence diagram has no direction to flip: %s", plain)
	}
}

func TestSequenceLayoutHasNoOverlaps(t *testing.T) {
	body := "client\nserver\ndb\nclient -> server: GET /p/1 with a long query string\nserver -> db: SELECT\n" +
		"db --> server: rows\nserver -> server: render the page\nserver --> client: 200 OK\nclient -> db: skips the server entirely\ndb -> db: vacuum"
	boxes := diagramBoxes(t, render(t, diagram("sequence", body)))
	if len(boxes) != 10 {
		t.Fatalf("got %d boxes, want 3 actors and 7 labels", len(boxes))
	}
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			if boxes[i].r.overlaps(boxes[j].r) {
				t.Errorf("%s %q overlaps %s %q", boxes[i].class, boxes[i].text, boxes[j].class, boxes[j].text)
			}
		}
	}
}

func TestFlowLayersPointDownward(t *testing.T) {
	out := render(t, diagram("flow", "a -> b\nb -> c\na -> c"))
	y := map[string]float64{}
	for _, b := range diagramBoxes(t, out) {
		y[b.text] = b.r.y
	}
	if y["a"] >= y["b"] || y["b"] >= y["c"] {
		t.Errorf("flow layers are not top to bottom: %v", y)
	}
}

func TestSequenceKeepsDeclaredOrder(t *testing.T) {
	out := render(t, diagram("sequence", "db\nclient\nclient -> db: q"))
	x := map[string]float64{}
	for _, b := range diagramBoxes(t, out) {
		x[b.text] = b.r.x
	}
	if x["db"] >= x["client"] {
		t.Errorf("participants not in declared order: %v", x)
	}
}

func TestDiagramNodesAreNeutralUnlessTheyHaveAState(t *testing.T) {
	arch := render(t, diagram("architecture", "a: Agent | built\nb: Daemon | review\nc: Hub | planned\nd: Disk | red\ne: Done | done\na -> b\nb -> c\nb -> d\nb -> e"))
	flow := render(t, diagram("flow", "a -> b\nb: failed"))
	seq := render(t, diagram("sequence", "a -> b: hi"))
	for _, out := range []string{arch, flow, seq} {
		if strings.Contains(out, "kapi-error") {
			t.Fatalf("an old or new tag broke the diagram:\n%s", out)
		}
	}
	for _, want := range []string{
		`<g class="kd-node kd-arch"><rect`,
		`<g class="kd-node kd-arch kd-planned"><rect`,
		`<g class="kd-node kd-arch kd-stated kapi-tone-green"><rect`,
		`<svg class="kd-state" `,
	} {
		if !strings.Contains(arch, want) {
			t.Errorf("architecture lacks %s:\n%s", want, arch)
		}
	}
	for _, gone := range []string{"kapi-tone-purple", "kapi-tone-yellow", "kapi-tone-red", ">built<", ">in review<", ">red</text>"} {
		if strings.Contains(arch, gone) {
			t.Errorf("architecture still shows %s:\n%s", gone, arch)
		}
	}
	if !strings.Contains(arch, `<figcaption class="kapi-diagram-legend"><span class="kapi-badge kapi-tone-green"><svg class="kapi-icon"`) || !strings.Contains(arch, `kd-legend-planned">planned</span>`) {
		t.Errorf("the legend should list the states in use with their icons, and planned:\n%s", arch)
	}
	if !strings.Contains(flow, `<g class="kd-node kd-stated kapi-tone-red"><rect`) || strings.Contains(flow, ">b: failed<") {
		t.Errorf("a flow node declared with a state should be drawn in that state:\n%s", flow)
	}
	if strings.Contains(seq, "kapi-tone") {
		t.Errorf("sequence actors should be neutral:\n%s", seq)
	}
	if out := render(t, diagram("flow", "a -> b\nb: wobble")); strings.Contains(out, "kapi-error") || strings.Contains(out, "kd-stated") {
		t.Errorf("an unknown tag should render neutral:\n%s", out)
	}
}

func TestDiagramKindsDrawTheirIconAboveTheLabel(t *testing.T) {
	for _, kind := range []string{"database", "user", "service", "page", "file", "queue", "timer", "external"} {
		out := render(t, diagram("architecture", "a: Store | rows | "+kind+"\nb: Plain\na -> b"))
		if !strings.Contains(out, `<svg class="kd-icon kd-kind-`+kind+`" `) {
			t.Errorf("%s: no kind icon:\n%s", kind, out)
		}
	}
	for _, dir := range dirs {
		out := render(t, diagramDir("architecture", dir, "db: Postgres | sessions | database done\napi: API | service\nweb: Web | page\napi -> db: reads\nweb -> api: calls"))
		boxes := diagramBoxes(t, out)
		assertNoOverlaps(t, dir+" kinds", boxes)
		var db, web box
		for _, b := range boxes {
			switch b.text {
			case "Postgres sessions":
				db = b
			case "Web":
				web = b
			}
		}
		if db.r.h < 90 || web.r.h < 70 {
			t.Errorf("%s: kind nodes should be tall enough for a large icon over the label: db %v web %v", dir, db.r, web.r)
		}
		icon := iconRect(t, out, "kd-kind-database")
		if icon.w < 40 || icon.w > 56 || icon.x < db.r.x || icon.x+icon.w > db.r.x+db.r.w || icon.y < db.r.y {
			t.Errorf("%s: the database icon %v should be 40 to 56px and sit inside its node %v", dir, icon, db.r)
		}
		if !strings.Contains(out, `kd-stated kapi-tone-green`) {
			t.Errorf("%s: database done lost its state:\n%s", dir, out)
		}
	}
	plain := render(t, diagram("flow", "a -> b"))
	if strings.Contains(plain, "kd-icon") {
		t.Errorf("nodes without a kind have no icon:\n%s", plain)
	}
	flow := render(t, diagram("flow", "app -> store: writes\nstore: database"))
	if !strings.Contains(flow, `kd-kind-database`) {
		t.Errorf("a flow node can be declared with a kind:\n%s", flow)
	}
}

func iconRect(t *testing.T, out, class string) rect {
	t.Helper()
	i := strings.Index(out, class+`"`)
	if i < 0 {
		t.Fatalf("no %s icon:\n%s", class, out)
	}
	tag := out[i : strings.Index(out[i:], ">")+i]
	num := func(k string) float64 {
		j := strings.Index(tag, " "+k+`="`)
		if j < 0 {
			t.Fatalf("%s icon has no %s: %s", class, k, tag)
		}
		v := tag[j+len(k)+3:]
		v = v[:strings.IndexByte(v, '"')]
		x, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	return rect{num("x"), num("y"), num("width"), num("height")}
}
