package page

import (
	"os"
	"syscall"
)

func inodeAndCtime(fi os.FileInfo) (uint64, int64) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return st.Ino, st.Ctim.Nano()
}
