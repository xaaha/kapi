package trash

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xaaha/kapi/internal/osfile"
)

type Item struct {
	Path string `json:"path"`
	At   string `json:"at"`
	Info string `json:"info,omitempty"`
}

var ErrOccupied = errors.New("something else is there now")

var rename = osfile.RenameNoReplace

type can struct {
	dir  string
	info string
}

func freedesktop(root string) can {
	return can{dir: filepath.Join(root, "files"), info: filepath.Join(root, "info")}
}

type Bin struct {
	cans func(path string) []can
}

func New(fallback string) *Bin {
	return &Bin{cans: func(path string) []can {
		home, err := os.UserHomeDir()
		if err != nil {
			return []can{freedesktop(fallback)}
		}
		return append(systemCans(home, path), freedesktop(fallback))
	}}
}

func Folder(dir string) *Bin {
	return &Bin{cans: func(string) []can { return []can{freedesktop(dir)} }}
}

func (b *Bin) Move(path string) (Item, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Item{}, err
	}
	if _, err := os.Lstat(path); err != nil {
		return Item{}, err
	}
	var errs []error
	for _, c := range b.cans(path) {
		it, err := c.put(path, time.Now())
		if err == nil {
			return it, nil
		}
		errs = append(errs, err)
	}
	return Item{}, fmt.Errorf("could not move %s to the Trash: %w", path, errors.Join(errs...))
}

const maxNames = 1000

func (c can) put(path string, now time.Time) (Item, error) {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return Item{}, err
	}
	if c.info != "" {
		if err := os.MkdirAll(c.info, 0o700); err != nil {
			return Item{}, err
		}
	}
	for n := 1; n <= maxNames; n++ {
		name := numbered(filepath.Base(path), n)
		at := filepath.Join(c.dir, name)
		info, err := c.reserve(name, path, now)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return Item{}, err
		}
		if err := rename(path, at); err != nil {
			release(info)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return Item{}, err
		}
		return Item{Path: path, At: at, Info: info}, nil
	}
	return Item{}, fmt.Errorf("%s holds %d items named like %s", c.dir, maxNames, filepath.Base(path))
}

func (c can) reserve(name, path string, now time.Time) (string, error) {
	if c.info == "" {
		return "", nil
	}
	info := filepath.Join(c.info, name+".trashinfo")
	f, err := os.OpenFile(info, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	body := "[Trash Info]\nPath=" + (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath() + "\nDeletionDate=" + now.Format("2006-01-02T15:04:05") + "\n"
	_, err = f.WriteString(body)
	if err = errors.Join(err, osfile.Sync(f), f.Close()); err != nil {
		release(info)
		return "", err
	}
	return info, nil
}

func release(info string) {
	if info != "" {
		_ = os.Remove(info)
	}
}

func numbered(name string, n int) string {
	if n == 1 {
		return name
	}
	ext := filepath.Ext(name)
	if ext == name {
		ext = ""
	}
	return strings.TrimSuffix(name, ext) + " " + strconv.Itoa(n) + ext
}

func Restore(it Item) error {
	if _, err := os.Lstat(it.Path); err == nil {
		return fmt.Errorf("%s: %w", it.Path, ErrOccupied)
	}
	if _, err := os.Lstat(it.At); err != nil {
		return fmt.Errorf("%s is no longer in the Trash: %w", filepath.Base(it.Path), err)
	}
	if err := os.MkdirAll(filepath.Dir(it.Path), 0o755); err != nil {
		return err
	}
	if err := rename(it.At, it.Path); errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s: %w", it.Path, ErrOccupied)
	} else if err != nil {
		return err
	}
	if it.Info != "" {
		if err := os.Remove(it.Info); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
