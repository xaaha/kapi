package trash

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOnLinuxAFileGoesToTheXDGTrashWithATrashinfoAndComesBack(t *testing.T) {
	data, work := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	page := filepath.Join(work, "plan.html")
	write(t, page, "mine")
	b := &Bin{cans: func(path string) []can { return systemCans(t.TempDir(), path) }}
	it, err := b.Move(page)
	if err != nil {
		t.Fatal(err)
	}
	if it.At != filepath.Join(data, "Trash", "files", "plan.html") || it.Info != filepath.Join(data, "Trash", "info", "plan.html.trashinfo") {
		t.Fatalf("item = %+v", it)
	}
	if info := read(t, it.Info); !strings.Contains(info, "\nPath="+page+"\n") {
		t.Fatalf("trashinfo = %q, want the absolute path", info)
	}
	if err := Restore(it); err != nil {
		t.Fatal(err)
	}
	if read(t, page) != "mine" {
		t.Fatal("not restored")
	}
	gone(t, it.Info)
}

func TestOnLinuxWithoutXDGDataHomeTheTrashIsUnderLocalShare(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	home := t.TempDir()
	cans := systemCans(home, filepath.Join(home, "plan.html"))
	if len(cans) != 1 || cans[0] != freedesktop(filepath.Join(home, ".local", "share", "Trash")) {
		t.Fatalf("cans = %+v", cans)
	}
}

func TestOnLinuxARelativeXDGDataHomeIsIgnored(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative/data")
	home := t.TempDir()
	if cans := systemCans(home, filepath.Join(home, "plan.html")); cans[0] != freedesktop(filepath.Join(home, ".local", "share", "Trash")) {
		t.Fatalf("cans = %+v", cans)
	}
}
