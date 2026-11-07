package daemon

import (
	"os"
	"runtime"
)

func modeIs(fi os.FileInfo, perm os.FileMode) bool {
	return runtime.GOOS == "windows" || fi.Mode().Perm() == perm
}

var tmpRoot = func() string {
	if runtime.GOOS == "windows" {
		return `C:\tmp`
	}
	return "/tmp"
}()
