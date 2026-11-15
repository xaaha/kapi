package trash

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is still there (err %v)", path, err)
	}
}

func TestAFileGoesToTheTrashWithItsInfoAndComesBack(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	page := filepath.Join(work, "my plan.html")
	write(t, page, "<h1>Plan</h1>")
	it, err := Folder(bin).Move(page)
	if err != nil {
		t.Fatal(err)
	}
	gone(t, page)
	if it.Path != page || it.At != filepath.Join(bin, "files", "my plan.html") || it.Info != filepath.Join(bin, "info", "my plan.html.trashinfo") {
		t.Fatalf("item = %+v", it)
	}
	if got := read(t, it.At); got != "<h1>Plan</h1>" {
		t.Fatalf("trashed file holds %q", got)
	}
	info := read(t, it.Info)
	want := regexp.MustCompile(`^\[Trash Info\]\nPath=\S*/my%20plan\.html\nDeletionDate=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\n$`)
	if !want.MatchString(info) {
		t.Fatalf("trashinfo = %q", info)
	}
	if err := Restore(it); err != nil {
		t.Fatal(err)
	}
	if got := read(t, page); got != "<h1>Plan</h1>" {
		t.Fatalf("restored file holds %q", got)
	}
	gone(t, it.At)
	gone(t, it.Info)
}

func TestAFolderGoesToTheTrashWholeAndComesBack(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	dir := filepath.Join(work, "plan_files")
	write(t, filepath.Join(dir, "a.png"), "png")
	write(t, filepath.Join(dir, "nested", "b.png"), "png2")
	it, err := Folder(bin).Move(dir)
	if err != nil {
		t.Fatal(err)
	}
	gone(t, dir)
	if got := read(t, filepath.Join(it.At, "nested", "b.png")); got != "png2" {
		t.Fatalf("trashed folder lost its contents: %q", got)
	}
	if err := Restore(it); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "a.png")) != "png" || read(t, filepath.Join(dir, "nested", "b.png")) != "png2" {
		t.Fatal("restored folder lost its contents")
	}
}

func TestTwoItemsWithTheSameNameGetDistinctPlacesInTheTrash(t *testing.T) {
	bin := t.TempDir()
	var items []Item
	for i, body := range []string{"one", "two", "three"} {
		page := filepath.Join(t.TempDir(), "plan.html")
		write(t, page, body)
		it, err := Folder(bin).Move(page)
		if err != nil {
			t.Fatalf("move %d: %v", i, err)
		}
		items = append(items, it)
	}
	names := []string{"plan.html", "plan 2.html", "plan 3.html"}
	for i, it := range items {
		if filepath.Base(it.At) != names[i] || filepath.Base(it.Info) != names[i]+".trashinfo" {
			t.Fatalf("item %d = %+v, want %s", i, it, names[i])
		}
	}
	if err := Restore(items[1]); err != nil {
		t.Fatal(err)
	}
	if got := read(t, items[1].Path); got != "two" {
		t.Fatalf("restored the wrong item: %q", got)
	}
}

func TestRestoreNeverReplacesWhatIsNowAtThePath(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	page := filepath.Join(work, "plan.html")
	write(t, page, "old")
	it, err := Folder(bin).Move(page)
	if err != nil {
		t.Fatal(err)
	}
	write(t, page, "new")
	if err := Restore(it); !errors.Is(err, ErrOccupied) {
		t.Fatalf("restore over a new file: %v, want ErrOccupied", err)
	}
	if read(t, page) != "new" || read(t, it.At) != "old" {
		t.Fatal("restore touched a file it refused")
	}
}

func TestRestoreSaysSoWhenTheTrashWasEmptied(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	page := filepath.Join(work, "plan.html")
	write(t, page, "x")
	it, err := Folder(bin).Move(page)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(bin, "files")); err != nil {
		t.Fatal(err)
	}
	err = Restore(it)
	if err == nil || !strings.Contains(err.Error(), "no longer in the Trash") {
		t.Fatalf("restore after emptying = %v", err)
	}
	gone(t, page)
}

func TestRestoreRecreatesAMissingParentFolder(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	page := filepath.Join(work, "docs", "plan.html")
	write(t, page, "x")
	it, err := Folder(bin).Move(page)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(page)); err != nil {
		t.Fatal(err)
	}
	if err := Restore(it); err != nil {
		t.Fatal(err)
	}
	if read(t, page) != "x" {
		t.Fatal("not restored")
	}
}

func TestMovingSomethingMissingFailsAndTouchesNothing(t *testing.T) {
	bin := t.TempDir()
	if _, err := Folder(bin).Move(filepath.Join(t.TempDir(), "nope.html")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("move of a missing file = %v", err)
	}
	if entries, _ := os.ReadDir(bin); len(entries) != 0 {
		t.Fatalf("the trash was written to: %v", entries)
	}
}

func TestATrashThatCannotTakeTheFileFallsThroughToTheNextAndLeavesNoInfoBehind(t *testing.T) {
	work, bad, good := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(bad, "files"), "a file where the folder should be")
	page := filepath.Join(work, "plan.html")
	write(t, page, "x")
	b := &Bin{cans: func(string) []can { return []can{freedesktop(bad), freedesktop(good)} }}
	it, err := b.Move(page)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(it.At) != filepath.Join(good, "files") {
		t.Fatalf("item went to %s", it.At)
	}
	if entries, _ := os.ReadDir(filepath.Join(bad, "info")); len(entries) != 0 {
		t.Fatalf("the failed trash kept info files: %v", entries)
	}
}

func TestWhenNoTrashCanTakeTheFileItStaysWhereItIs(t *testing.T) {
	work, bad := t.TempDir(), t.TempDir()
	write(t, filepath.Join(bad, "files"), "blocker")
	page := filepath.Join(work, "plan.html")
	write(t, page, "x")
	if _, err := Folder(bad).Move(page); err == nil || !strings.Contains(err.Error(), "could not move") {
		t.Fatalf("move = %v, want an error", err)
	}
	if read(t, page) != "x" {
		t.Fatal("the file moved or changed after a failed trash")
	}
}

func TestTheFallbackFolderIsTheLastTrashTried(t *testing.T) {
	fallback := filepath.Join(t.TempDir(), "trash")
	cans := New(fallback).cans(filepath.Join(t.TempDir(), "plan.html"))
	if last := cans[len(cans)-1]; last != freedesktop(fallback) {
		t.Fatalf("last trash = %+v, want the fallback", last)
	}
}

func racing(t *testing.T, create func(dst string)) {
	t.Helper()
	moved := rename
	t.Cleanup(func() { rename = moved })
	rename = func(src, dst string) error {
		create(dst)
		return moved(src, dst)
	}
}

func TestRestoreNeverReplacesAFileWrittenAtThePathJustBeforeTheMove(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	page := filepath.Join(work, "plan.html")
	write(t, page, "old")
	it, err := Folder(bin).Move(page)
	if err != nil {
		t.Fatal(err)
	}
	racing(t, func(dst string) { write(t, dst, "written by an agent") })
	if err := Restore(it); !errors.Is(err, ErrOccupied) {
		t.Fatalf("restore over a file that appeared mid-restore = %v, want ErrOccupied", err)
	}
	if read(t, page) != "written by an agent" || read(t, it.At) != "old" {
		t.Fatal("the restore replaced the new file or lost the trashed one")
	}
}

func TestMoveNeverReplacesAnItemThatLandsInTheTrashUnderTheSameName(t *testing.T) {
	work, home := t.TempDir(), t.TempDir()
	page := filepath.Join(work, "plan.html")
	write(t, page, "mine")
	trashDir := filepath.Join(home, ".Trash")
	b := &Bin{cans: func(string) []can { return []can{{dir: trashDir}} }}
	once := false
	racing(t, func(dst string) {
		if !once {
			once = true
			write(t, dst, "someone else's")
		}
	})
	it, err := b.Move(page)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(trashDir, "plan.html")) != "someone else's" {
		t.Fatal("the move replaced an item already in the Trash")
	}
	if it.At != filepath.Join(trashDir, "plan 2.html") || read(t, it.At) != "mine" {
		t.Fatalf("item = %+v", it)
	}
}

func TestAFolderNeverReplacesAnEmptyFolderThatAppearsAtTheTarget(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	dir := filepath.Join(work, "plan.files")
	write(t, filepath.Join(dir, "a.png"), "png")
	it, err := Folder(bin).Move(dir)
	if err != nil {
		t.Fatal(err)
	}
	racing(t, func(dst string) {
		if err := os.MkdirAll(dst, 0o700); err != nil {
			t.Fatal(err)
		}
	})
	if err := Restore(it); !errors.Is(err, ErrOccupied) {
		t.Fatalf("restore over an empty folder that appeared = %v, want ErrOccupied", err)
	}
	if read(t, filepath.Join(it.At, "a.png")) != "png" {
		t.Fatal("the trashed folder moved")
	}
}
