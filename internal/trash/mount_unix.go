//go:build darwin || linux

package trash

import (
	"os"
	"path/filepath"
	"syscall"
)

func mountRoot(path string) string {
	dir := filepath.Dir(path)
	dev, ok := device(dir)
	for !ok {
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
		dev, ok = device(dir)
	}
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		if d, ok := device(parent); !ok || d != dev {
			return dir
		}
		dir = parent
	}
}

func device(path string) (any, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, false
	}
	return st.Dev, true
}
