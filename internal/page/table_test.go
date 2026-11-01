package page

import (
	"errors"
	"strings"
	"testing"
)

func row(n int) *int { return &n }

func TestADeleteRowOpRemovesOnlyThatRowAndUndoes(t *testing.T) {
	home, path := newPage(t, tablePage)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "deleterow", ID: "aaaaaaaa", Row: row(1)})
	got := onDisk(t, path)
	if strings.Contains(got, "beta") || !strings.Contains(got, "alpha") || !strings.Contains(got, "<th scope=\"col\">Shard</th>") {
		t.Fatalf("after deleting row 1:\n%s", got)
	}
	mustApply(t, s, &Op{Kind: "undo"})
	if got := onDisk(t, path); got != tablePage {
		t.Errorf("undo did not restore the row:\n%s", got)
	}
}

func TestADeleteRowOpRefusesTheHeaderAndMissingRows(t *testing.T) {
	const bare = "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><th>A</th></tr><tr><td>x</td></tr></table></kapi-table>\n"
	for _, tc := range []struct {
		page string
		row  *int
		want error
	}{
		{tablePage, row(0), ErrHeaderRow},
		{bare, row(0), ErrHeaderRow},
		{tablePage, row(3), nil},
		{tablePage, row(-1), nil},
		{tablePage, nil, nil},
	} {
		home, path := newPage(t, tc.page)
		s := open(t, home, path)
		_, _, err := s.Apply(1, &Op{Kind: "deleterow", ID: "aaaaaaaa", Row: tc.row})
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("deleterow %v on %q: err = %v, want %v", tc.row, tc.page, err, tc.want)
		}
		if got := onDisk(t, path); got != tc.page {
			t.Errorf("refused deleterow changed the file:\n%s", got)
		}
	}
}

func TestADeleteRowOpWorksOnAPlainTable(t *testing.T) {
	const src = "<table data-kapi-id=\"aaaaaaaa\"><tr><th>A</th></tr><tr><td>x</td></tr><tr><td>y</td></tr></table>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "deleterow", ID: "aaaaaaaa", Row: row(2)})
	if got, want := onDisk(t, path), "<table data-kapi-id=\"aaaaaaaa\"><tr><th>A</th></tr><tr><td>x</td></tr></table>\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOuterRowKeepsTheHeaderAndOnlyThatRow(t *testing.T) {
	got, err := OuterRow([]byte(tablePage), "aaaaaaaa", 2)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); !strings.Contains(s, "Shard") || !strings.Contains(s, "alpha") || strings.Contains(s, "beta") || strings.Contains(s, "bbbbbbbb") {
		t.Errorf("OuterRow row 2:\n%s", s)
	}
	if _, err := OuterRow([]byte(tablePage), "aaaaaaaa", 3); err == nil {
		t.Error("OuterRow past the last row: want an error")
	}
}

func col(n int) *int { return &n }

func TestADeleteColOpRemovesThatColumnFromEveryRowAndUndoes(t *testing.T) {
	home, path := newPage(t, tablePage)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "deletecol", ID: "aaaaaaaa", Col: col(0)})
	got := onDisk(t, path)
	for _, gone := range []string{"Shard", "beta", "alpha"} {
		if strings.Contains(got, gone) {
			t.Errorf("column 0 still has %q:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "<th>Rows</th>") || !strings.Contains(got, "10") || !strings.Contains(got, "9") {
		t.Errorf("column 1 lost:\n%s", got)
	}
	mustApply(t, s, &Op{Kind: "undo"})
	if got := onDisk(t, path); got != tablePage {
		t.Errorf("undo did not restore the column:\n%s", got)
	}
}

func TestADeleteColOpRefusesMergedCellsTheLastColumnAndMissingColumns(t *testing.T) {
	const merged = "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><th>A</th><th>B</th></tr><tr><td colspan=\"2\">x</td></tr></table></kapi-table>\n"
	const single = "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><th>A</th></tr><tr><td>x</td></tr></table></kapi-table>\n"
	for _, tc := range []struct {
		page string
		col  *int
	}{{merged, col(0)}, {single, col(0)}, {tablePage, col(2)}, {tablePage, col(-1)}, {tablePage, nil}} {
		home, path := newPage(t, tc.page)
		s := open(t, home, path)
		if _, _, err := s.Apply(1, &Op{Kind: "deletecol", ID: "aaaaaaaa", Col: tc.col}); err == nil {
			t.Errorf("deletecol %v on %q: want an error", tc.col, tc.page)
		}
		if got := onDisk(t, path); got != tc.page {
			t.Errorf("refused deletecol changed the file:\n%s", got)
		}
	}
}

func TestOuterColKeepsOnlyThatColumn(t *testing.T) {
	got, err := OuterCol([]byte(tablePage), "aaaaaaaa", 1)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); !strings.Contains(s, "Rows") || !strings.Contains(s, "10") || strings.Contains(s, "Shard") || strings.Contains(s, "beta") {
		t.Errorf("OuterCol 1:\n%s", s)
	}
}
