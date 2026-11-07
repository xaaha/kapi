// Package osfile reads, renames and flushes files the same way on every system kapi runs on.
package osfile

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

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

// ReadFile shares delete access so an agent can rename over a page while kapi reads it.
func ReadFile(name string) ([]byte, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	var h windows.Handle
	err = retry(func() (err error) {
		h, err = windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		return err
	})
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	f := os.NewFile(uintptr(h), name)
	defer f.Close()
	return io.ReadAll(f)
}

// Rename retries while another program briefly holds the target open, as editors and virus scanners do.
func Rename(oldpath, newpath string) error {
	return retry(func() error { return os.Rename(oldpath, newpath) })
}

const busyWait = 2 * time.Second

func retry(f func() error) error {
	start := time.Now()
	for delay := time.Millisecond; ; delay = min(2*delay, 100*time.Millisecond) {
		err := f()
		if err == nil || !busy(err) || time.Since(start) > busyWait {
			return err
		}
		time.Sleep(delay)
	}
}

func busy(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
