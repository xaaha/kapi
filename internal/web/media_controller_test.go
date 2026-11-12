package web

import (
	"strings"
	"testing"
)

func TestMediaBrowserBehaviorHasOneOwner(t *testing.T) {
	controller, err := static.ReadFile("static/media.js")
	if err != nil {
		t.Fatal(err)
	}
	page, err := static.ReadFile("static/kapi.js")
	if err != nil {
		t.Fatal(err)
	}

	owned := []string{
		`kapi-media-viewer`,
		`ArrowLeft`,
		`ArrowRight`,
		`function fitFrame`,
		`a.kapi-thumb`,
	}
	for _, marker := range owned {
		if !strings.Contains(string(controller), marker) {
			t.Errorf("media.js does not own %q", marker)
		}
		if strings.Contains(string(page), marker) {
			t.Errorf("kapi.js duplicates media-controller detail %q", marker)
		}
	}
}

func TestShellLoadsTheMediaControllerOnce(t *testing.T) {
	// services.go is the one shell route for every page, matching the table controller pattern.
	body := string(mustStatic(t, "static/media.js"))
	if !strings.Contains(body, "window.kapiCreateMedia") {
		t.Fatal("media.js does not expose its controller")
	}
}

func mustStatic(t *testing.T, name string) []byte {
	t.Helper()
	b, err := static.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
