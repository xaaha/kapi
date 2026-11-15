package trash

import (
	"os"
	"path/filepath"
	"strconv"
)

func systemCans(home, path string) []can {
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		data = filepath.Join(home, ".local", "share")
	}
	cans := []can{freedesktop(filepath.Join(data, "Trash"))}
	if top := mountRoot(path); top != "" && top != mountRoot(data) {
		cans = append(cans, freedesktop(filepath.Join(top, ".Trash-"+strconv.Itoa(os.Getuid()))))
	}
	return cans
}
