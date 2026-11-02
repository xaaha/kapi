package web

import (
	"strings"
	"testing"
)

func TestTableBrowserBehaviorHasOneOwner(t *testing.T) {
	controller, err := static.ReadFile("static/tables.js")
	if err != nil {
		t.Fatal(err)
	}
	page, err := static.ReadFile("static/kapi.js")
	if err != nil {
		t.Fatal(err)
	}

	owned := []string{
		`data-kapi-wide`,
		`data-kapi-tight`,
		`data-kapi-fit`,
		`kapi-expanded-tables:`,
		`function stickHeaders`,
		`function sort(`,
		`function pickedCell(`,
	}
	for _, marker := range owned {
		if !strings.Contains(string(controller), marker) {
			t.Errorf("tables.js does not own %q", marker)
		}
		if strings.Contains(string(page), marker) {
			t.Errorf("kapi.js duplicates table-controller detail %q", marker)
		}
	}
}
