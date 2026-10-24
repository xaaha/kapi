package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var typedMark = regexp.MustCompile(` zq\d+`)

var idTag = regexp.MustCompile(`<[a-z][\w-]*\b[^>]*\bdata-kapi-id="([^"]+)"[^>]*>`)

func tagsByID(src string) map[string]string {
	out := map[string]string{}
	for _, m := range idTag.FindAllStringSubmatch(src, -1) {
		out[m[1]] = m[0]
	}
	return out
}

func TestEveryTextPartOfTheGalleryEditsInPlaceInChrome(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	gallery, err := os.ReadFile("../render/testdata/gallery.html")
	if err != nil {
		t.Fatal(err)
	}
	s := newSite(t, string(gallery))
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "browsertest/gallery-edit.mjs", s.srv.URL+"/"+token+"/p/"+s.id+"/").Output()
	if err != nil {
		t.Fatalf("gallery-edit.mjs: %v\n%s", err, out)
	}
	var report struct {
		Skip   string
		Edited []struct{ Key, Mark string }
		Raw    []string
		Blocks []string
		Errors []string
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("report %q: %v", out, err)
	}
	if report.Skip != "" {
		t.Skip(report.Skip)
	}
	for _, e := range report.Errors {
		t.Error(e)
	}
	if len(report.Edited) < 40 || len(report.Raw) < 3 || len(report.Blocks) < 5 {
		t.Errorf("edited %d text parts, opened %d raw ones and %d whole blocks; the gallery has more", len(report.Edited), len(report.Raw), len(report.Blocks))
	}
	after, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range report.Edited {
		if !strings.Contains(string(after), e.Mark+"<") && !strings.Contains(string(after), e.Mark+"\n") && !strings.Contains(string(after), e.Mark+`"`) {
			t.Errorf("%s: %q is not in the file", e.Key, e.Mark)
		}
	}
	was, is := tagsByID(string(before)), tagsByID(typedMark.ReplaceAllString(string(after), ""))
	for id, tag := range was {
		if is[id] != tag {
			t.Errorf("#%s: %s became %s", id, tag, is[id])
		}
	}
	if len(is) != len(was) {
		t.Errorf("ids: %d before, %d after", len(was), len(is))
	}
}
