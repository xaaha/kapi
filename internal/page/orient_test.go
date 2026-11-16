package page

import (
	"errors"
	"strings"
	"testing"
)

const diagrams = "<kapi-diagram data-kapi-id=\"ffffffff\">\na -> b\n</kapi-diagram>\n" +
	"<kapi-diagram type=\"architecture\" dir=\"lr\" data-kapi-id=\"aaaaaaaa\">\na: A\n</kapi-diagram>\n" +
	"<kapi-diagram type=\"sequence\" data-kapi-id=\"ssssssss\">\na -> b: hi\n</kapi-diagram>\n" +
	"<kapi-diagram type=\"gantt\" data-kapi-id=\"gggggggg\">\na -> b\n</kapi-diagram>\n" +
	"<p data-kapi-id=\"pppppppp\">p</p>\n"

func TestOrientSetsTheDiagramsDir(t *testing.T) {
	home, path := newPage(t, diagrams)
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "orient", ID: "ffffffff", Dir: "lr"})
	if sum.Text != "drew left to right" || len(sum.IDs) != 1 || sum.IDs[0] != "ffffffff" {
		t.Errorf("summary = %+v", sum)
	}
	mustApply(t, s, &Op{Kind: "orient", ID: "aaaaaaaa", Dir: "tb"})
	got := onDisk(t, path)
	for _, want := range []string{
		"<kapi-diagram dir=\"lr\" data-kapi-id=\"ffffffff\">\na -> b\n</kapi-diagram>",
		"<kapi-diagram type=\"architecture\" dir=\"tb\" data-kapi-id=\"aaaaaaaa\">",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("file lacks %q:\n%s", want, got)
		}
	}
}

func TestOrientRefusesBadTargetsAndDirs(t *testing.T) {
	home, path := newPage(t, diagrams)
	s := open(t, home, path)
	for _, op := range []Op{
		{Kind: "orient", ID: "ffffffff", Dir: "up"},
		{Kind: "orient", ID: "ffffffff"},
		{Kind: "orient", ID: "ssssssss", Dir: "lr"},
		{Kind: "orient", ID: "gggggggg", Dir: "lr"},
		{Kind: "orient", ID: "pppppppp", Dir: "lr"},
	} {
		if _, _, err := s.Apply(1, &op); err == nil || errors.Is(err, ErrStale) {
			t.Errorf("%+v: err = %v, want a refusal", op, err)
		}
	}
	if got := onDisk(t, path); got != diagrams {
		t.Errorf("refused orients changed the file:\n%s", got)
	}
}

const mermaidBlocks = "<pre class=\"mermaid\" data-kapi-id=\"mmmmmmmm\">flowchart TD\n  a[Web] --> b[(Orders)]\n</pre>\n" +
	"<pre data-kapi-id=\"cccccccc\"><code class=\"language-mermaid\">%% keep &amp; this\ngraph;\n  a --> b</code></pre>\n" +
	"<pre class=\"mermaid\" data-kapi-id=\"qqqqqqqq\">sequenceDiagram\n  a->>b: hi</pre>\n" +
	"<pre class=\"mermaid\" data-kapi-id=\"uuuuuuuu\">flowchart TD\n  subgraph one\n  a --> b\n  end</pre>\n" +
	"<pre data-kapi-id=\"oooooooo\">flowchart TD\n  a --> b</pre>\n"

func TestOrientRewritesOnlyTheMermaidDirectionWord(t *testing.T) {
	home, path := newPage(t, mermaidBlocks)
	s := open(t, home, path)
	if sum := mustApply(t, s, &Op{Kind: "orient", ID: "mmmmmmmm", Dir: "lr"}); sum.Text != "drew left to right" {
		t.Errorf("summary = %+v", sum)
	}
	mustApply(t, s, &Op{Kind: "orient", ID: "cccccccc", Dir: "lr"})
	want := strings.Replace(strings.Replace(mermaidBlocks, "flowchart TD\n  a[Web]", "flowchart LR\n  a[Web]", 1), "graph;", "graph LR;", 1)
	if got := onDisk(t, path); got != want {
		t.Fatalf("file after orient:\n%s\nwant:\n%s", got, want)
	}
	mustApply(t, s, &Op{Kind: "orient", ID: "mmmmmmmm", Dir: "tb"})
	if got := onDisk(t, path); !strings.Contains(got, "flowchart TB\n  a[Web]") {
		t.Errorf("flipping back should write TB:\n%s", got)
	}
	for range 3 {
		mustApply(t, s, &Op{Kind: "undo"})
	}
	if got := onDisk(t, path); got != mermaidBlocks {
		t.Errorf("undo did not restore the original:\n%s", got)
	}
}

func TestOrientRefusesMermaidItCannotFlip(t *testing.T) {
	home, path := newPage(t, mermaidBlocks)
	s := open(t, home, path)
	for _, id := range []string{"qqqqqqqq", "uuuuuuuu", "oooooooo"} {
		if _, _, err := s.Apply(1, &Op{Kind: "orient", ID: id, Dir: "lr"}); err == nil || errors.Is(err, ErrStale) {
			t.Errorf("orient #%s: err = %v, want a refusal", id, err)
		}
	}
	if got := onDisk(t, path); got != mermaidBlocks {
		t.Errorf("refused orients changed the file:\n%s", got)
	}
}

func TestOrientIsStaleOnlyWhenItsDiagramChanged(t *testing.T) {
	home, path := newPage(t, diagrams)
	s := open(t, home, path)
	externalWrite(t, path, strings.Replace(diagrams, ">p<", ">p, by the agent<", 1))
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(1, &Op{Kind: "orient", ID: "ffffffff", Dir: "lr"}); err != nil {
		t.Fatalf("orient at rev 1 after another block changed: %v", err)
	}
	if _, _, err := s.Apply(2, &Op{Kind: "orient", ID: "ffffffff", Dir: "tb"}); !errors.Is(err, ErrStale) {
		t.Errorf("orient at rev 2 after the diagram itself changed: err = %v, want ErrStale", err)
	}
}
