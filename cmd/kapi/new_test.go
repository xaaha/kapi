package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xaaha/kapi/internal/daemon"
	pkgpage "github.com/xaaha/kapi/internal/page"
)

func scratchLibrary(t *testing.T) (kapiHome, drafts string) {
	t.Helper()
	kapiHome = t.TempDir()
	root := filepath.Join(t.TempDir(), "kapi")
	t.Setenv("KAPI_HOME", kapiHome)
	if err := os.WriteFile(filepath.Join(kapiHome, "settings.json"), []byte(`{"library":"`+filepath.ToSlash(root)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return kapiHome, filepath.Join(root, "drafts")
}

func TestNewCreatesADraftPrintsItsPathAndOpensIt(t *testing.T) {
	_, drafts := scratchLibrary(t)
	opened := stubBrowser(t, nil)
	const url = "http://127.0.0.1:5000/tok/p/0123456789abcdef/"
	f := &fakeAPI{resp: daemon.Response{URL: url}}
	out, err := run(t, f, "new", "Rollout plan: Q3 & beyond")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(drafts, "rollout-plan-q3-beyond.html")
	if out != path+"\n"+url+"\n" {
		t.Errorf("output = %q, want the path then the URL", out)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(src) != "<h1>Rollout plan: Q3 &amp; beyond</h1>\n" {
		t.Errorf("draft = %q", src)
	}
	if want := (daemon.Request{Op: "open", Page: path, Dir: abs(t, ".")}); len(f.requests) != 1 || !reflect.DeepEqual(f.requests[0], want) {
		t.Errorf("requests = %+v, want %+v", f.requests, want)
	}
	if len(*opened) != 1 || (*opened)[0] != url {
		t.Errorf("opened = %v", *opened)
	}
}

func TestNewJoinsUnquotedWordsAndPrintOnlyLeavesTheBrowserAlone(t *testing.T) {
	_, drafts := scratchLibrary(t)
	opened := stubBrowser(t, nil)
	out, err := run(t, &fakeAPI{resp: daemon.Response{URL: "http://127.0.0.1:5000/tok/p/x/"}}, "new", "--print", "Auth", "review")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, filepath.Join(drafts, "auth-review.html")+"\n") || len(*opened) != 0 {
		t.Errorf("output %q, opened %v", out, *opened)
	}
}

func TestNewNeverOverwritesAndSkipsNamesKapiStillTracks(t *testing.T) {
	kapiHome, drafts := scratchLibrary(t)
	stubBrowser(t, nil)
	if err := os.MkdirAll(drafts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drafts, "plan.html"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(drafts, "plan 2.files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(kapiHome, "pages", pkgpage.ID(filepath.Join(drafts, "plan 3.html"))), 0o700); err != nil {
		t.Fatal(err)
	}
	var made []string
	for range 2 {
		out, err := run(t, &fakeAPI{resp: daemon.Response{URL: "u"}}, "new", "--print", "Plan")
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, strings.SplitN(out, "\n", 2)[0])
	}
	if want := []string{filepath.Join(drafts, "plan 4.html"), filepath.Join(drafts, "plan 5.html")}; !reflect.DeepEqual(made, want) {
		t.Errorf("made %q, want %q", made, want)
	}
	if b, _ := os.ReadFile(filepath.Join(drafts, "plan.html")); string(b) != "mine" {
		t.Errorf("plan.html = %q, want it untouched", b)
	}
}

func TestNewNeedsATitle(t *testing.T) {
	scratchLibrary(t)
	f := &fakeAPI{}
	if _, err := run(t, f, "new", "  "); err == nil || !strings.Contains(err.Error(), "title") {
		t.Errorf("err = %v, want one asking for a title", err)
	}
	if len(f.requests) != 0 {
		t.Errorf("requests = %+v, want none", f.requests)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Rollout plan":            "rollout-plan",
		"  --Ünïcode café--  ":    "ünïcode-café",
		"!!!":                     "untitled",
		"a/b\\c":                  "a-b-c",
		strings.Repeat("ab ", 60): strings.TrimSuffix(strings.Repeat("ab-", 27), "-"),
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
