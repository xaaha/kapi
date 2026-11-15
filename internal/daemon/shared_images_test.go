package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/xaaha/kapi/internal/page"
)

func TestSavingAPageWhoseImagesAnotherPageUsesCopiesThem(t *testing.T) {
	refs := map[string]string{
		"img src":  `<img src="./plan.files/a.png">`,
		"srcset":   `<img srcset="small.png 1x, plan.files/a.png 2x">`,
		"a href":   `<a href="plan.files/a.png">full size</a>`,
		"css url":  `<kapi-render><div style="background: url('./plan.files/a.png')"></div></kapi-render>`,
		"escaped":  `<img src="./plan%2Efiles/a.png">`,
		"absolute": `<img src="ABS">`,
	}
	for name, ref := range refs {
		t.Run(name, func(t *testing.T) {
			home, s, _ := serving(t)
			lib := withLibrary(t, s)
			from, img := draftWithImage(t)
			if name == "absolute" {
				ref = `<img src="` + filepath.ToSlash(filepath.Join(page.FilesDir(from), "a.png")) + `">`
			}
			other := filepath.Join(filepath.Dir(from), "other.html")
			if err := os.WriteFile(other, []byte("<h1>Other</h1>\n"+ref+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			call(t, home, &Request{Op: "open", Page: from})
			call(t, home, &Request{Op: "open", Page: other})
			to, err := s.Save(from, "Work")
			if err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{page.FilesDir(from), filepath.Join(lib, "Work", "plan.files")} {
				if b, err := os.ReadFile(filepath.Join(dir, "a.png")); err != nil || !bytes.Equal(b, img) {
					t.Errorf("%s/a.png = %q, %v; want the image in both places", dir, b, err)
				}
			}
			if present(filepath.Join(s.pageState(to), intentName)) {
				t.Errorf("%s left behind", intentName)
			}
		})
	}
}

func TestSavingAPageWhoseImagesNoOtherPageUsesMovesThem(t *testing.T) {
	home, s, _ := serving(t)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	other := filepath.Join(filepath.Dir(from), "other.html")
	if err := os.WriteFile(other, []byte(`<h1>Other</h1><img src="https://example.com/plan.files/a.png"><a href="#plan.files">x</a><img src="./other.files/b.png">`), 0o600); err != nil {
		t.Fatal(err)
	}
	call(t, home, &Request{Op: "open", Page: from})
	call(t, home, &Request{Op: "open", Page: other})
	if _, err := s.Save(from, "Work"); err != nil {
		t.Fatal(err)
	}
	if present(page.FilesDir(from)) {
		t.Error("the images folder was copied although no other page uses it")
	}
}

func TestUndoingASharedCopyLeavesTheOriginalImages(t *testing.T) {
	home, s, _ := serving(t)
	lib := withLibrary(t, s)
	from, img := draftWithImage(t)
	other := filepath.Join(filepath.Dir(from), "other.html")
	if err := os.WriteFile(other, []byte(`<img src="./plan.files/a.png">`), 0o600); err != nil {
		t.Fatal(err)
	}
	call(t, home, &Request{Op: "open", Page: from})
	call(t, home, &Request{Op: "open", Page: other})
	hook(t, &removePath, func(p string) error {
		if p == from {
			return errInjected
		}
		return os.Remove(p)
	})
	if _, err := s.Save(from, "Work"); err == nil {
		t.Fatal("Save succeeded")
	}
	if b, err := os.ReadFile(filepath.Join(page.FilesDir(from), "a.png")); err != nil || !bytes.Equal(b, img) {
		t.Errorf("original image = %q, %v", b, err)
	}
	assertNothingInLibrary(t, lib)
}
