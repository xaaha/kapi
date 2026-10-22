package render

import (
	"fmt"
	"strings"
	"testing"
)

func archSources() map[string]string {
	srcs := map[string]string{
		"reference": "agent: Agent | kapi watch | built\nplan: plan.html | page source | built\ndaemon: kapi daemon | event log | page store | review\n" +
			"disk: ~/.kapi | pages | built\nbrowser: Browser page | toolbar | review\nhub: Private hub | R2 | planned\n" +
			"agent -> plan: edits file\nplan <-> daemon: reads, writes ops, notices your edits\nagent <-> daemon: socket\n" +
			"browser <-> daemon: HTTP + SSE\ndaemon -> disk: append\ndaemon --> hub: publish",
		"pair":      "a: Client\nb: Server\na <-> b: HTTP",
		"long flat": "a: A\nb: B\nc: C\na <-> b: a rather long two way label\nb <-> c: and another long one here\na <-> c: around the middle one",
		"cycle":     "a: A\nb: B\nc: C\na -> b: one\nb -> c: two\nc -> a: three",
		"two-way":   "a: A\nb: B\nc: C\nd: D\na <-> b: ab\nb <-> c: bc\nc <-> d: cd\nd <-> a: da\na <-> c: ac",
		"self loop": "a: A | sub\nb: B\na -> a: retries itself\na -> b: then calls",
	}
	seed := uint32(11)
	pick := func(n int) int {
		seed = seed*1103515245 + 12345
		return int(seed>>16) % n
	}
	arrows := []string{"->", "<->", "-->"}
	states := []string{"built", "review", "planned", "danger", "info", ""}
	for g := 0; g < 16; g++ {
		n := 4 + g%9
		var lines []string
		for i := 0; i < n; i++ {
			state := states[pick(len(states))]
			if state != "" {
				state = " | " + state
			}
			lines = append(lines, fmt.Sprintf("n%d: Service number %d | handles part %d of the work%s", i, i, pick(9), state))
		}
		for i := 1; i < n; i++ {
			lines = append(lines, fmt.Sprintf("n%d %s n%d: link %d", pick(i), arrows[pick(3)], i, i))
		}
		for k := 0; k < n/2; k++ {
			a, b := pick(n), pick(n)
			label := ""
			if k%2 == 0 {
				label = ": extra"
			}
			lines = append(lines, fmt.Sprintf("n%d %s n%d%s", a, arrows[pick(3)], b, label))
		}
		srcs[fmt.Sprintf("random %d", g)] = strings.Join(lines, "\n")
	}
	return srcs
}

func TestArchitectureLayoutHasNoOverlaps(t *testing.T) {
	for name, body := range archSources() {
		for _, dir := range dirs {
			out := render(t, diagramDir("architecture", dir, body))
			if strings.Contains(out, "kapi-error") {
				t.Fatalf("%s %s: %s", dir, name, out)
			}
			assertNoOverlaps(t, dir+" "+name, diagramBoxes(t, out))
			assertParseStable(t, name, out)
		}
	}
}

func TestArchitectureLayoutIsDeterministic(t *testing.T) {
	for name, body := range archSources() {
		for _, dir := range dirs {
			src := diagramDir("architecture", dir, body)
			first := render(t, src)
			for i := 0; i < 10; i++ {
				if render(t, src) != first {
					t.Fatalf("%s %s: render %d differs from the first", dir, name, i)
				}
			}
		}
	}
}

func TestArchitectureRunsLeftToRightByDefault(t *testing.T) {
	out := render(t, diagram("architecture", "a: A\nb: B\nc: C\na -> b\nb -> c"))
	x := map[string]float64{}
	for _, b := range diagramBoxes(t, out) {
		x[b.text] = b.r.x
	}
	if x["A"] >= x["B"] || x["B"] >= x["C"] || !strings.Contains(out, `data-kapi-dir="lr"`) {
		t.Errorf("architecture should run left to right: %v", x)
	}
	out = render(t, diagramDir("architecture", "tb", "a: A\nb: B\na -> b"))
	y := map[string]float64{}
	for _, b := range diagramBoxes(t, out) {
		y[b.text] = b.r.y
	}
	if y["A"] >= y["B"] {
		t.Errorf("dir=tb should run top to bottom: %v", y)
	}
}

func TestArchitectureNodesShowStateAndTone(t *testing.T) {
	out := render(t, diagram("architecture", "a: Agent | one | two | built\nb: Daemon | review\nc: Hub | planned\nd: Disk | danger\ne: Plain\n"+
		"a <-> b: socket\nb --> c\nb -> d\nb -> e"))
	for _, want := range []string{
		`<g class="kd-node kd-arch kapi-tone-accent"><rect`,
		`<g class="kd-node kd-arch kapi-tone-warning"><rect`,
		`<g class="kd-node kd-arch kapi-tone-neutral kd-planned"><rect`,
		`<g class="kd-node kd-arch kapi-tone-danger"><rect`,
		`<g class="kd-node kd-arch kapi-tone-neutral"><rect`,
		`class="kd-title"`, `>Agent</text>`, `class="kd-sub"`, `>one</text>`, `>two</text>`,
		`marker-start="url(#`, `class="kd-edge kd-dashed"`,
		`<figcaption class="kapi-diagram-legend"><span class="kapi-badge kapi-tone-accent">built</span> <span class="kapi-badge kapi-tone-warning">in review</span> <span class="kapi-badge kapi-tone-neutral kd-legend-planned">planned</span></figcaption>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, ">built</text>") || strings.Contains(out, ">danger</text>") {
		t.Errorf("a state was drawn as a subtitle:\n%s", out)
	}
	if plain := render(t, diagram("architecture", "a: A | danger\nb: B\na -> b")); strings.Contains(plain, "figcaption") {
		t.Errorf("tones alone need no legend: %s", plain)
	}
}

func TestArchitectureRefusesBadSource(t *testing.T) {
	for src, want := range map[string]string{
		"a: A\na -> ghost":       `no node "ghost"`,
		"a: A\na: B":             `"a" is declared twice`,
		"just a line":            "line 1: expected",
		"bad id!: T":             "line 1: node id",
		"a: \nb: B":              "line 1: node \"a\" needs a title",
		"# only a comment":       "has no nodes",
		"a: A\nb: B\na <--> b":   "line 3",
		"a: A\nb: B\na - > b: x": "line 3",
	} {
		out := render(t, diagram("architecture", src))
		if !strings.Contains(out, "kapi-error") || !strings.Contains(out, escText(want)) {
			t.Errorf("source %q: want an error box with %q:\n%s", src, want, out)
		}
	}
}

func TestWideDiagramsAskForMoreRoom(t *testing.T) {
	long := " | a subtitle long enough to make the node wide"
	wide := render(t, diagram("architecture", "a: A"+long+"\nb: B"+long+"\nc: C"+long+"\na -> b: first\nb -> c: second"))
	if !strings.Contains(wide, `class="kapi-diagram kapi-diagram-architecture kapi-diagram-wide"`) {
		t.Errorf("a diagram wider than the text column should be marked wide: %.200s", wide)
	}
	if narrow := render(t, diagram("flow", "a -> b")); strings.Contains(narrow, "kapi-diagram-wide") {
		t.Errorf("a small diagram is not wide: %s", narrow)
	}
	if css := string(CSS()); !strings.Contains(css, ".kapi-diagram-wide {") || !strings.Contains(css, "container-type: inline-size") {
		t.Error("CSS does not let wide diagrams grow past the text column")
	}
}
