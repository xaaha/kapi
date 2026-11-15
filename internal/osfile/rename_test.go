package osfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func renameCases(t *testing.T, rename func(oldpath, newpath string) error) {
	t.Helper()
	dir := t.TempDir()
	put := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	src, taken := put("src", "mine"), put("taken", "theirs")
	if err := rename(src, taken); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("rename onto a file = %v, want ErrExist", err)
	}
	if b, _ := os.ReadFile(taken); string(b) != "theirs" {
		t.Fatal("the existing file was replaced")
	}
	srcDir, emptyDir := filepath.Join(dir, "srcdir"), filepath.Join(dir, "empty")
	for _, d := range []string{srcDir, emptyDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join("srcdir", "a"), "a")
	if err := rename(srcDir, emptyDir); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("rename onto an empty folder = %v, want ErrExist", err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "a")); err != nil {
		t.Fatal("the folder moved after a refused rename")
	}
	free := filepath.Join(dir, "free")
	if err := rename(src, free); err != nil {
		t.Fatal(err)
	}
	if err := rename(srcDir, filepath.Join(dir, "freedir")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "freedir", "a")); string(b) != "a" {
		t.Fatal("the folder did not move")
	}
	if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the source is still there")
	}
}

func TestRenameNoReplaceRefusesAnythingAtTheTarget(t *testing.T) {
	renameCases(t, RenameNoReplace)
}
