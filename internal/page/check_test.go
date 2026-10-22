package page

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const checklist = "<kapi-checklist data-kapi-id=\"llllllll\">\n" +
	"  <kapi-task data-kapi-id=\"aaaaaaaa\">Copy</kapi-task>\n" +
	"  <kapi-task done data-kapi-id=\"bbbbbbbb\">Flip</kapi-task>\n" +
	"  <kapi-task data-kapi-id=\"cccccccc\" done=\"\">Delete</kapi-task>\n" +
	"</kapi-checklist>\n<p data-kapi-id=\"pppppppp\">p</p>\n"

func TestCheckTicksAndUnticksATask(t *testing.T) {
	home, path := newPage(t, checklist)
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "check", ID: "aaaaaaaa", Done: true})
	if sum.Text != "checked" || !slices.Equal(sum.IDs, []string{"aaaaaaaa"}) {
		t.Errorf("summary = %+v", sum)
	}
	mustApply(t, s, &Op{Kind: "check", ID: "bbbbbbbb", Done: false})
	sum = mustApply(t, s, &Op{Kind: "check", ID: "cccccccc", Done: false})
	if sum.Text != "unchecked" {
		t.Errorf("summary = %+v", sum)
	}
	if rev, sum, err := s.Apply(4, &Op{Kind: "check", ID: "aaaaaaaa", Done: true}); err != nil || rev != 4 || sum.Text != "" {
		t.Errorf("check of a ticked task: rev %d, %+v, %v; want no change", rev, sum, err)
	}
	want := "<kapi-checklist data-kapi-id=\"llllllll\">\n" +
		"  <kapi-task done data-kapi-id=\"aaaaaaaa\">Copy</kapi-task>\n" +
		"  <kapi-task data-kapi-id=\"bbbbbbbb\">Flip</kapi-task>\n" +
		"  <kapi-task data-kapi-id=\"cccccccc\">Delete</kapi-task>\n" +
		"</kapi-checklist>\n<p data-kapi-id=\"pppppppp\">p</p>\n"
	if got := onDisk(t, path); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
}

func TestCheckRefusesAnythingButATask(t *testing.T) {
	home, path := newPage(t, checklist)
	s := open(t, home, path)
	for _, id := range []string{"llllllll", "pppppppp"} {
		if _, _, err := s.Apply(1, &Op{Kind: "check", ID: id, Done: true}); err == nil || !strings.Contains(err.Error(), "kapi-task") {
			t.Errorf("check #%s: err = %v, want a refusal naming kapi-task", id, err)
		}
	}
	if _, _, err := s.Apply(1, &Op{Kind: "check", ID: "zzzzzzzz", Done: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("check of a missing id: err = %v", err)
	}
	if got := onDisk(t, path); got != checklist {
		t.Errorf("refused checks changed the file:\n%s", got)
	}
}

func TestCheckIsStaleOnlyWhenItsTaskChanged(t *testing.T) {
	home, path := newPage(t, checklist)
	s := open(t, home, path)
	externalWrite(t, path, strings.Replace(checklist, ">p<", ">p, by the agent<", 1))
	if _, _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(1, &Op{Kind: "check", ID: "aaaaaaaa", Done: true}); err != nil {
		t.Fatalf("check at rev 1 after another block changed: %v", err)
	}
	if _, _, err := s.Apply(2, &Op{Kind: "check", ID: "aaaaaaaa", Done: false}); !errors.Is(err, ErrStale) {
		t.Errorf("check at rev 2 after the task itself changed: err = %v, want ErrStale", err)
	}
}

func TestUncheckRemovesEveryDoneOnTheTask(t *testing.T) {
	home, path := newPage(t, "<kapi-checklist data-kapi-id=\"llllllll\">\n  <kapi-task done data-kapi-id=\"aaaaaaaa\" done=\"\">Copy</kapi-task>\n</kapi-checklist>\n")
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "check", ID: "aaaaaaaa", Done: false})
	want := "<kapi-checklist data-kapi-id=\"llllllll\">\n  <kapi-task data-kapi-id=\"aaaaaaaa\">Copy</kapi-task>\n</kapi-checklist>\n"
	if got := onDisk(t, path); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
}
