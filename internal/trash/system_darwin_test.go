package trash

import (
	"path/filepath"
	"testing"
)

func TestOnMacAFileGoesToTheHomeTrashFolderUnderAFreeNameAndComesBack(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".Trash", "plan.html"), "already trashed")
	page := filepath.Join(work, "plan.html")
	write(t, page, "mine")
	b := &Bin{cans: func(path string) []can { return systemCans(home, path) }}
	it, err := b.Move(page)
	if err != nil {
		t.Fatal(err)
	}
	if it.At != filepath.Join(home, ".Trash", "plan 2.html") || it.Info != "" {
		t.Fatalf("item = %+v", it)
	}
	gone(t, page)
	if read(t, filepath.Join(home, ".Trash", "plan.html")) != "already trashed" {
		t.Fatal("an item already in the Trash was replaced")
	}
	if err := Restore(it); err != nil {
		t.Fatal(err)
	}
	if read(t, page) != "mine" {
		t.Fatal("not restored")
	}
}

func TestOnMacAFileOnTheHomeVolumeHasOnlyTheHomeTrash(t *testing.T) {
	home := t.TempDir()
	cans := systemCans(home, filepath.Join(t.TempDir(), "plan.html"))
	if len(cans) != 1 || cans[0].dir != filepath.Join(home, ".Trash") {
		t.Fatalf("cans = %+v", cans)
	}
}
