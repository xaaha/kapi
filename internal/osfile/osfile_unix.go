//go:build !windows

// Package osfile reads, renames and flushes files the same way on every system kapi runs on.
package osfile

import "os"

func Sync(f *os.File) error { return f.Sync() }

func ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
