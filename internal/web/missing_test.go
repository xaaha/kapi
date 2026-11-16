package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func (e *sse) named(want string) string {
	e.t.Helper()
	for {
		id, data := e.raw()
		if id == "named" && data == "event: "+want {
			_, data = e.raw()
			return data
		}
	}
}

func TestAMissingPageOffersItsNewestVersionAndRestoresIt(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	want, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	page := s.get("/p/" + s.id + "/")
	if page.code != http.StatusOK || !strings.Contains(page.body, `data-missing="{&#34;id&#34;:&#34;`+s.id) || !strings.Contains(page.body, `&#34;rev&#34;:1`) {
		t.Fatalf("missing page: %d\n%s", page.code, page.body)
	}
	code, out := s.post("/messages", `{"kind":"message","text":"hi"}`)
	if m, _ := out["missing"].(map[string]any); code != http.StatusGone || m["rev"] != 1.0 {
		t.Fatalf("message to a missing page: %d %v, want 410 naming rev 1", code, out)
	}
	code, out = s.post("/recover", `{}`)
	if code != http.StatusOK || out["rev"] != 1.0 {
		t.Fatalf("recover: %d %v", code, out)
	}
	if got, err := os.ReadFile(s.path); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("restored file = %q, %v; want %q", got, err, want)
	}
	if page := s.get("/p/" + s.id + "/"); page.code != http.StatusOK || strings.Contains(page.body, "data-missing") {
		t.Fatalf("page after recover: %d\n%s", page.code, page.body)
	}
	if code, _ := s.post("/recover", `{}`); code != http.StatusConflict {
		t.Errorf("recover over a file that is back: %d, want 409", code)
	}
}

func TestTheStreamTellsATabWhenTheFileGoesAndComesBack(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live := s.stream(ctx, "")
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Rev uint64 `json:"rev"`
	}
	if err := json.Unmarshal([]byte(live.named("missing")), &m); err != nil || m.Rev != 1 {
		t.Fatalf("missing event: %+v, %v", m, err)
	}
	waiting := s.stream(ctx, "")
	waiting.named("missing")
	if code, _ := s.post("/recover", `{}`); code != http.StatusOK {
		t.Fatalf("recover: %d", code)
	}
	var back struct {
		Rev      uint64 `json:"rev"`
		Restored bool   `json:"restored"`
	}
	if err := json.Unmarshal([]byte(waiting.named("present")), &back); err != nil || back.Rev != 1 || !back.Restored {
		t.Fatalf("present event: %+v, %v", back, err)
	}
}

func TestAFileWrittenAgainIsNotCalledRestored(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := s.stream(ctx, "")
	waiting.named("missing")
	if err := os.WriteFile(s.path, []byte(`<p data-kapi-id="bbbbbbbb">new</p>`), 0o600); err != nil {
		t.Fatal(err)
	}
	var back struct {
		Rev      uint64 `json:"rev"`
		Restored bool   `json:"restored"`
	}
	if err := json.Unmarshal([]byte(waiting.named("present")), &back); err != nil || back.Restored || back.Rev != 2 {
		t.Fatalf("present event: %+v, %v; want rev 2, not restored", back, err)
	}
}
