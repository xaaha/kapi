package trash

import (
	"os"
	"path/filepath"
	"strconv"
)

func systemCans(home, path string) []can {
	cans := []can{{dir: filepath.Join(home, ".Trash")}}
	top := mountRoot(path)
	if top == "" || top == mountRoot(home) {
		return cans
	}
	if fi, err := os.Lstat(filepath.Join(top, ".Trashes")); err == nil && fi.IsDir() {
		cans = append(cans, can{dir: filepath.Join(top, ".Trashes", strconv.Itoa(os.Getuid()))})
	}
	return cans
}
