package trash

import (
	"path/filepath"
	"testing"
)

func TestOnWindowsAFileGoesToTheFallbackFolderWithATrashinfoAndComesBack(t *testing.T) {
	if cans := systemCans(t.TempDir(), `C:\work\plan.html`); len(cans) != 0 {
		t.Fatalf("Windows has a system trash in kapi: %+v", cans)
	}
	work, fallback := t.TempDir(), t.TempDir()
	page := filepath.Join(work, "plan.html")
	images := filepath.Join(work, "plan_files")
	write(t, page, "mine")
	write(t, filepath.Join(images, "a.png"), "png")
	b := New(fallback)
	pageItem, err := b.Move(page)
	if err != nil {
		t.Fatal(err)
	}
	imagesItem, err := b.Move(images)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(pageItem.At) != filepath.Join(fallback, "files") || filepath.Dir(imagesItem.Info) != filepath.Join(fallback, "info") {
		t.Fatalf("items = %+v %+v", pageItem, imagesItem)
	}
	gone(t, page)
	gone(t, images)
	for _, it := range []Item{pageItem, imagesItem} {
		if err := Restore(it); err != nil {
			t.Fatal(err)
		}
	}
	if read(t, page) != "mine" || read(t, filepath.Join(images, "a.png")) != "png" {
		t.Fatal("not restored")
	}
}
