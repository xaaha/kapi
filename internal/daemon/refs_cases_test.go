package daemon

import (
	"path/filepath"
	"testing"
)

func TestRefersIntoFindsAReferenceIntoAFolderInEveryPlaceAPageCanPointAtAFile(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, "plan.files")
	for _, tc := range []struct {
		name, src string
		want      bool
	}{
		{"img src", `<p><img src="plan.files/a.png"></p>`, true},
		{"dot slash", `<img src="./plan.files/a.png">`, true},
		{"srcset", `<img src="other.png" srcset="small.png 1x, plan.files/big.png 2x">`, true},
		{"a href", `<a href="plan.files/report.pdf#page=2">report</a>`, true},
		{"style url", `<div style="background: url('plan.files/bg.png') no-repeat"></div>`, true},
		{"style element", `<style>.hero { background-image: url(plan.files/hero.png?v=2); }</style>`, true},
		{"escaped", `<img src="plan%2Efiles/a%20b.png">`, true},
		{"absolute path", `<img src="` + filepath.ToSlash(filepath.Join(dir, "a.png")) + `">`, true},
		{"from a subfolder", `<img src="../plan.files/a.png">`, false},
		{"another folder", `<img src="notes.files/a.png">`, false},
		{"a prefix that is not the folder", `<img src="plan.files2/a.png">`, false},
		{"the folder name in text", `<p>plan.files/a.png</p>`, false},
		{"web address", `<img src="https://example.com/plan.files/a.png">`, false},
		{"data url", `<img src="data:image/png;base64,plan.files">`, false},
		{"nothing", `<p>hello</p>`, false},
	} {
		if got := refersInto([]byte(tc.src), work, dir); got != tc.want {
			t.Errorf("%s: refersInto = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRefersIntoResolvesRelativeLinksFromThePagesOwnFolder(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, "plan.files")
	if !refersInto([]byte(`<img src="../plan.files/a.png">`), filepath.Join(work, "sub"), dir) {
		t.Fatal("a page in a subfolder pointing up into the folder was missed")
	}
}
