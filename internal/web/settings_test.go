package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/settings"
)

func (s *site) patchSettings(body string, header http.Header) got {
	s.t.Helper()
	r, err := http.NewRequest(http.MethodPatch, s.srv.URL+"/"+token+"/settings", strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	r.Header.Set("Origin", s.srv.URL)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		r.Header[k] = v
	}
	resp, err := s.client.Do(r)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	if _, err := bufio.NewReader(resp.Body).WriteTo(&b); err != nil {
		s.t.Fatal(err)
	}
	return got{resp.StatusCode, resp.Header, b.String()}
}

func decodeSettings(t *testing.T, body string) settings.Settings {
	t.Helper()
	var out settings.Settings
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not settings JSON: %v\n%s", err, body)
	}
	return out
}

func TestSettingsStartAtTheDefaults(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	resp := s.get("/settings")
	if resp.code != http.StatusOK {
		t.Fatalf("GET /settings: %d %s", resp.code, resp.body)
	}
	if got := decodeSettings(t, resp.body); got.Mode != "system" || got.Theme != "default" {
		t.Errorf("GET /settings = %+v, want system and default", got)
	}
}

func TestPatchingSettingsSavesThemAndRendersThemIntoEveryPage(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	resp := s.patchSettings(`{"mode":"dark","theme":"paper"}`, nil)
	if resp.code != http.StatusOK {
		t.Fatalf("PATCH /settings: %d %s", resp.code, resp.body)
	}
	if got := decodeSettings(t, resp.body); got.Mode != "dark" || got.Theme != "paper" || got.Recent[0] != "paper" {
		t.Errorf("PATCH answered %+v", got)
	}
	if got := decodeSettings(t, s.get("/settings").body); got.Mode != "dark" || got.Theme != "paper" {
		t.Errorf("GET after PATCH = %+v", got)
	}
	for _, path := range []string{"/", "/p/" + s.id + "/"} {
		page := s.get(path).body
		if !strings.Contains(page, `<html lang="en" data-theme-choice="dark" data-palette-choice="paper" data-palette-recent="paper default">`) {
			t.Errorf("%s does not carry the saved theme on <html>:\n%.300s", path, page)
		}
	}
}

func TestPatchingSettingsRefusesUnknownValues(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, body := range []string{`{"mode":"sepia"}`, `{"theme":"neon"}`} {
		if resp := s.patchSettings(body, nil); resp.code != http.StatusUnprocessableEntity || !strings.Contains(resp.body, "invalid setting") {
			t.Errorf("PATCH %s: %d %s, want 422", body, resp.code, resp.body)
		}
	}
	if resp := s.patchSettings(`{"mode":`, nil); resp.code != http.StatusBadRequest {
		t.Errorf("PATCH with a broken body: %d, want 400", resp.code)
	}
}

func TestPatchingSettingsNeedsThisPageAsTheOrigin(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	for _, h := range []http.Header{{"Origin": {"http://evil.example"}}, {"Content-Type": {"text/plain"}}} {
		if resp := s.patchSettings(`{"mode":"dark"}`, h); resp.code != http.StatusForbidden {
			t.Errorf("PATCH with %v: %d, want 403", h, resp.code)
		}
	}
	if got := decodeSettings(t, s.get("/settings").body); got.Mode != "system" {
		t.Errorf("a refused PATCH changed the mode to %q", got.Mode)
	}
}

func TestSettingsEventsSendTheCurrentValueThenEveryChange(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, s.srv.URL+"/"+token+"/settings/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	lines := bufio.NewScanner(resp.Body)
	next := func() settings.Settings {
		t.Helper()
		for lines.Scan() {
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				return decodeSettings(t, data)
			}
		}
		t.Fatalf("stream ended: %v", lines.Err())
		return settings.Settings{}
	}
	if got := next(); got.Theme != "default" {
		t.Errorf("first event %+v, want the current settings", got)
	}
	s.patchSettings(`{"theme":"paper"}`, nil)
	if got := next(); got.Theme != "paper" {
		t.Errorf("after PATCH the stream sent %+v", got)
	}
	s.patchSettings(`{"mode":"light"}`, nil)
	if got := next(); got.Mode != "light" || got.Theme != "paper" {
		t.Errorf("after the second PATCH the stream sent %+v", got)
	}
}
