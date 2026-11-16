package page

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xaaha/kapi/internal/osfile"
)

var ErrNoCopy = errors.New("kapi kept no copy of this page")

// Copy is the newest version of a page kapi kept under its state directory.
type Copy struct {
	Rev  uint64
	At   time.Time
	File string
}

func LatestCopy(home, path string) (Copy, error) {
	dir := filepath.Join(home, "pages", ID(path))
	st, err := readState(dir)
	if err != nil || st == nil || st.Rev == 0 {
		return Copy{}, err
	}
	name := filepath.Join(dir, "revs", strconv.FormatUint(st.Rev, 10)+".html")
	fi, err := os.Stat(name)
	if err != nil {
		return Copy{}, err
	}
	return Copy{Rev: st.Rev, At: fi.ModTime(), File: name}, nil
}

// Recover writes the newest copy back to path, recreating its folder, and fails with fs.ErrExist when a file is already there.
func Recover(home, path string) (Copy, error) {
	c, err := LatestCopy(home, path)
	if err != nil {
		return Copy{}, err
	}
	if c.Rev == 0 {
		return Copy{}, ErrNoCopy
	}
	src, err := osfile.ReadFile(c.File)
	if err != nil {
		return Copy{}, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the page's own folder, as it was before
		return Copy{}, err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+tempMark+"*")
	if err != nil {
		return Copy{}, err
	}
	tmp := f.Name()
	_, err = f.Write(src)
	err = errors.Join(err, f.Chmod(0o644), osfile.Sync(f), f.Close())
	if err == nil {
		err = osfile.RenameNoReplace(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return Copy{}, err
	}
	return c, nil
}
