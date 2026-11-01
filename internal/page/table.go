package page

import (
	"errors"
	"fmt"
)

var ErrHeaderRow = errors.New("the header row cannot be deleted")

func deleteRow(src []byte, els []element, e *element, row *int) ([]byte, Summary, error) {
	if row == nil {
		return nil, Summary{}, errors.New("deleterow needs a row")
	}
	trs, cells := tableRows(els, e), cellsOf(els, e)
	r := *row
	if r < 0 || r >= len(trs) {
		return nil, Summary{}, fmt.Errorf("#%s has no row %d", e.id, r)
	}
	if isHeaderRow(els, trs[r], cells[r], r) {
		return nil, Summary{}, ErrHeaderRow
	}
	if err := linkedInto(els, trs[r]); err != nil {
		return nil, Summary{}, err
	}
	text := "deleted a row of " + describe(src, e)
	return cut(src, trs[r]), Summary{Text: text, IDs: []string{e.id}}, nil
}

func isHeaderRow(els []element, tr *element, cells []*element, r int) bool {
	if tr.parent >= 0 && els[tr.parent].name == "thead" {
		return true
	}
	if r != 0 || len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if c.name != "th" {
			return false
		}
	}
	return true
}

func tableRows(els []element, e *element) []*element {
	table := -1
	var rows []*element
	for i := range els {
		c := &els[i]
		switch {
		case !e.contains(c):
		case c.name == "table" && table < 0:
			table = i
		case c.name == "tr" && table >= 0 && nearest(els, i, "table") == table:
			rows = append(rows, c)
		}
	}
	return rows
}

func OuterRow(src []byte, id string, row int) ([]byte, error) {
	els := parse(src)
	e, err := find(els, id)
	if err != nil {
		return nil, err
	}
	trs, cells := tableRows(els, e), cellsOf(els, e)
	if row < 0 || row >= len(trs) {
		return nil, fmt.Errorf("#%s has no row %d", id, row)
	}
	out := src
	for i := len(trs) - 1; i >= 0; i-- {
		if i != row && !isHeaderRow(els, trs[i], cells[i], i) {
			out = cut(out, trs[i])
		}
	}
	return Outer(out, id)
}

var ErrMergedCells = errors.New("the table has merged cells, so a single column cannot be cut out of it")

func deleteCol(src []byte, els []element, e *element, col *int) ([]byte, Summary, error) {
	if col == nil {
		return nil, Summary{}, errors.New("deletecol needs a col")
	}
	cells, err := columnCells(els, e, *col)
	if err != nil {
		return nil, Summary{}, err
	}
	if widest(els, e) < 2 {
		return nil, Summary{}, errors.New("the last column cannot be deleted; delete the table instead")
	}
	if err := linkedInto(els, cells...); err != nil {
		return nil, Summary{}, err
	}
	text := "deleted a column of " + describe(src, e)
	out := src
	for i := len(cells) - 1; i >= 0; i-- {
		out = cut(out, cells[i])
	}
	return out, Summary{Text: text, IDs: []string{e.id}}, nil
}

func OuterCol(src []byte, id string, col int) ([]byte, error) {
	els := parse(src)
	e, err := find(els, id)
	if err != nil {
		return nil, err
	}
	keep, err := columnCells(els, e, col)
	if err != nil {
		return nil, err
	}
	kept := map[*element]bool{}
	for _, c := range keep {
		kept[c] = true
	}
	out := src
	rows := cellsOf(els, e)
	for r := len(rows) - 1; r >= 0; r-- {
		for c := len(rows[r]) - 1; c >= 0; c-- {
			if !kept[rows[r][c]] {
				out = cut(out, rows[r][c])
			}
		}
	}
	return Outer(out, id)
}

func columnCells(els []element, e *element, col int) ([]*element, error) {
	rows := cellsOf(els, e)
	if col < 0 || col >= widest(els, e) {
		return nil, fmt.Errorf("#%s has no column %d", e.id, col)
	}
	var cells []*element
	for _, row := range rows {
		for _, c := range row {
			if span, _ := c.attr("colspan"); span != "" && span != "1" {
				return nil, ErrMergedCells
			}
			if span, _ := c.attr("rowspan"); span != "" && span != "1" {
				return nil, ErrMergedCells
			}
		}
		if col < len(row) {
			cells = append(cells, row[col])
		}
	}
	return cells, nil
}

func widest(els []element, e *element) int {
	n := 0
	for _, row := range cellsOf(els, e) {
		n = max(n, len(row))
	}
	return n
}
