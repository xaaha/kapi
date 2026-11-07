//go:build !windows

// Package durable flushes files and directories to disk before kapi relies on them.
package durable

import "os"

func Sync(f *os.File) error { return f.Sync() }
