//go:build !linux && !darwin && !windows

package osfile

func RenameNoReplace(oldpath, newpath string) error { return renameExclusive(oldpath, newpath) }
