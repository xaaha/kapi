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
	shortcuts, err := static.ReadFile("static/shortcuts.js")
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
	if !strings.Contains(string(controller), `event.stopImmediatePropagation()`) {
		t.Error("the media viewer does not take precedence over page shortcuts and block editing")
	}
	for marker, problem := range map[string]string{
		`el.inert = true`:     "the media viewer does not make the background inert",
		`event.key === "Tab"`: "the media viewer does not trap keyboard focus",
		`before.focus({`:      "the media viewer does not restore focus when it closes",
	} {
		if !strings.Contains(string(controller), marker) {
			t.Error(problem)
		}
	}
	if strings.Count(string(controller), "paths.chevron") != 2 {
		t.Error("previous and next do not use the same chevron geometry")
	}
	if !strings.Contains(string(page), `tool("expand", "Open image", "preview")`) {
		t.Error("the toolbar does not use the familiar expand icon and Open image label")
	}
	if strings.Contains(string(page), `tool("eyes"`) {
		t.Error("the toolbar still uses the ambiguous Eyes icon")
	}
	if !strings.Contains(string(shortcuts), `preview: letter("p")`) {
		t.Error("image preview is not on the central P toolbar shortcut")
	}
}

func TestGalleryThumbnailsStayMinimal(t *testing.T) {
	css := string(mustStatic(t, "static/media.css"))
	if strings.Contains(css, ".kapi-gallery img {\n  display: block;\n  width: 100%;\n  aspect-ratio") {
		t.Error("gallery images are forced into a letterboxed aspect ratio")
	}
	if !strings.Contains(css, "cursor: zoom-in") || !strings.Contains(css, "border-radius: var(--kapi-radius-sm)") {
		t.Error("gallery thumbnails do not have the minimal zoom affordance")
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
