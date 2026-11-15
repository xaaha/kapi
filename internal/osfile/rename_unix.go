//go:build !windows

package osfile

import (
	"errors"
	"os"
	"syscall"
)

func renameExclusive(oldpath, newpath string) error {
	fi, err := os.Lstat(oldpath)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		if err := os.Link(oldpath, newpath); err != nil {
			return err
		}
		return os.Remove(oldpath)
	}
	return placeholderDir(oldpath, newpath)
}

func placeholderDir(oldpath, newpath string) error {
	if err := os.Mkdir(newpath, 0o700); err != nil {
		return err
	}
	if err := syscall.Rename(oldpath, newpath); err != nil {
		return errors.Join(&os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}, os.Remove(newpath))
	}
	return nil
}
