//go:build !darwin && !linux

package page

import "os"

func inodeAndCtime(os.FileInfo) (uint64, int64) { return 0, 0 }
