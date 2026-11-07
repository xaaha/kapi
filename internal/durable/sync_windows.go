// Package durable flushes files and directories to disk before kapi relies on them.
package durable

import "os"

// Sync skips directories: Windows refuses to flush a directory handle, and NTFS journals renames itself.
func Sync(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	return f.Sync()
}
