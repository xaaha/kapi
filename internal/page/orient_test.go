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
