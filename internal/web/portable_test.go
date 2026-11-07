package web

import (
	"os"
	"runtime"
)

func modeIs(fi os.FileInfo, perm os.FileMode) bool {
	return runtime.GOOS == "windows" || fi.Mode().Perm() == perm
}
