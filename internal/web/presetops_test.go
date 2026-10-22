package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestACheckOpTicksTheTaskInTheFile(t *testing.T) {
	s := newSite(t, "<kapi-checklist data-kapi-id=\"llllllll\">\n<kapi-task data-kapi-id=\"aaaaaaaa\">Copy</kapi-task>\n</kapi-checklist>\n")
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"check","id":"aaaaaaaa","done":true}}`)
	if code != http.StatusOK || out["rev"] != 2.0 {
		t.Fatalf("check: %d %v", code, out)
	}
	if b, _ := os.ReadFile(s.path); !strings.Contains(string(b), `<kapi-task done data-kapi-id="aaaaaaaa">`) {
		t.Errorf("file after check:\n%s", b)
	}
	if ev := s.events(); len(ev) != 1 || ev[0].Kind != "revision" || ev[0].Target != "aaaaaaaa" || ev[0].Text != "checked" {
		t.Errorf("events = %+v", ev)
	}
	if b := s.get("/p/" + s.id + "/blocks/aaaaaaaa"); b.code != http.StatusOK || !strings.Contains(b.body, `<input type="checkbox" checked=""`) {
		t.Errorf("block after check: %d %s", b.code, b.body)
	}
	code, _ = s.post("/ops", `{"rev":2,"op":{"kind":"check","id":"aaaaaaaa"}}`)
	if b, _ := os.ReadFile(s.path); code != http.StatusOK || strings.Contains(string(b), "done") {
		t.Errorf("uncheck: %d, file:\n%s", code, b)
	}
	code, out = s.post("/ops", `{"rev":3,"op":{"kind":"check","id":"llllllll","done":true}}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "kapi-task") {
		t.Errorf("check of the list itself: %d %v, want 422", code, out)
	}
}

func TestAnOrientOpFlipsTheDiagram(t *testing.T) {
	s := newSite(t, "<kapi-diagram data-kapi-id=\"ffffffff\">\na -> b\n</kapi-diagram>\n")
	code, out := s.post("/ops", `{"rev":1,"op":{"kind":"orient","id":"ffffffff","dir":"lr"}}`)
	if code != http.StatusOK || out["rev"] != 2.0 {
		t.Fatalf("orient: %d %v", code, out)
	}
	if b, _ := os.ReadFile(s.path); !strings.Contains(string(b), `<kapi-diagram dir="lr" data-kapi-id="ffffffff">`) {
		t.Errorf("file after orient:\n%s", b)
	}
	if ev := s.events(); len(ev) != 1 || ev[0].Target != "ffffffff" {
		t.Errorf("events = %+v", ev)
	}
	if b := s.get("/p/" + s.id + "/blocks/ffffffff"); !strings.Contains(b.body, `data-kapi-dir="lr"`) {
		t.Errorf("block after orient: %s", b.body)
	}
	code, out = s.post("/ops", `{"rev":2,"op":{"kind":"orient","id":"ffffffff","dir":"diagonal"}}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "tb or lr") {
		t.Errorf("bad dir: %d %v, want 422", code, out)
	}
}
