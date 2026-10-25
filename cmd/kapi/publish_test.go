package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/publish"
)

func TestPublishUploadsThePageAndPrintsItsURL(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotAuth, gotBody = r.URL.Path, r.Header.Get("Authorization"), string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	t.Setenv("KAPI_HOME", t.TempDir())
	t.Setenv("KAPI_HUB_URL", srv.URL)
	t.Setenv("KAPI_HUB_TOKEN", "cli-token")
	page := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(page, []byte("<p>ship it</p>"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, &fakeAPI{}, "publish", page)
	if err != nil {
		t.Fatal(err)
	}
	id := publish.PageID(page)
	if want := srv.URL + "/p/" + id + "/\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	if !regexp.MustCompile(`^/api/pages/` + id + `/revs/[0-9]+$`).MatchString(gotPath) {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer cli-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, "<p>ship it</p>") || !strings.Contains(gotBody, "<title>plan.html</title>") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestPublishWithoutAHubSaysHowToConfigureOne(t *testing.T) {
	t.Setenv("KAPI_HOME", t.TempDir())
	t.Setenv("KAPI_HUB_URL", "")
	t.Setenv("KAPI_HUB_TOKEN", "")
	page := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(page, []byte("<p>x</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, &fakeAPI{}, "publish", page)
	if err == nil || !strings.Contains(err.Error(), "hub.json") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishMissingPageFails(t *testing.T) {
	t.Setenv("KAPI_HUB_URL", "https://hub.example")
	t.Setenv("KAPI_HUB_TOKEN", "x")
	if _, err := run(t, &fakeAPI{}, "publish", filepath.Join(t.TempDir(), "nope.html")); err == nil {
		t.Fatal("want error")
	}
}

func TestPublishRefusesAPageWhoseImagesAreMissing(t *testing.T) {
	uploads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		uploads++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	t.Setenv("KAPI_HOME", t.TempDir())
	t.Setenv("KAPI_HUB_URL", srv.URL)
	t.Setenv("KAPI_HUB_TOKEN", "cli-token")
	page := filepath.Join(t.TempDir(), "plan.html")
	name := "ffffffffffffffffffffffffffffffff.png"
	if err := os.WriteFile(page, []byte(`<figure data-kapi-id="aaaaaaaa"><img src="./plan.files/`+name+`" alt=""></figure>`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, &fakeAPI{}, "publish", page)
	if err == nil || !strings.Contains(err.Error(), "missing from plan.files: "+name) {
		t.Fatalf("err = %v, want the missing image named", err)
	}
	if uploads != 0 {
		t.Errorf("uploaded %d times", uploads)
	}
}
