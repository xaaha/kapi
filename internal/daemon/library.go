package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/settings"
)

const (
	movedName  = "moved.txt"
	intentName = "move.json"
	maxFolders = 500
)

type moveIntent struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) folders() (settings.Folders, error) {
	prefs, err := s.prefs.Get(context.Background())
	if err != nil {
		return settings.Folders{}, err
	}
	return prefs.Folders()
}

func (s *Server) stateDir(path string) string {
	return filepath.Join(s.home, "pages", page.ID(path))
}

func (s *Server) Library() (string, []string, error) {
	f, err := s.folders()
	if err != nil {
		return "", nil, err
	}
	var out []string
	err = filepath.WalkDir(f.Library, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == f.Library && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if path == f.Library || !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(f.Library, path)
		if err != nil {
			return err
		}
		if settings.CheckFolderName(d.Name()) != nil {
			return fs.SkipDir
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= maxFolders {
			return fs.SkipAll
		}
		if strings.Count(rel, string(filepath.Separator)) >= settings.FolderDepth-1 {
			return fs.SkipDir
		}
		return nil
	})
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return Tilde(f.Library), out, err
}

func (s *Server) Save(path, folder string) (string, error) {
	rel, err := settings.CheckFolder(folder)
	if err != nil {
		return "", err
	}
	f, err := s.folders()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(f.Library, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the library is the person's own folder
		return "", err
	}
	to, err := s.move(path, dir)
	s.notebookChanged()
	return to, err
}

func (s *Server) Unsave(path string) error {
	st, _, err := s.store(path)
	if err != nil {
		return err
	}
	defer s.notebookChanged()
	return st.SetSaved(false)
}

func (s *Server) move(from, dir string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if to, moved := s.movedTo(from); moved {
		if _, err := os.Lstat(from); errors.Is(err, fs.ErrNotExist) {
			return "", &page.MovedError{From: from, To: to}
		}
	}
	info, err := os.Stat(from)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", from)
	}
	if resolved(filepath.Dir(from)) == resolved(dir) {
		if st := s.stores[from]; st != nil {
			return from, st.SetSaved(true)
		}
		return from, page.MarkSaved(s.stateDir(from))
	}
	if st := s.stores[from]; st != nil {
		if err := st.Retire(); err != nil {
			return "", err
		}
		delete(s.stores, from)
	}
	if err := os.MkdirAll(s.stateDir(from), 0o700); err != nil {
		return "", err
	}
	var to string
	for n := 1; ; n++ {
		to = filepath.Join(dir, page.Numbered(filepath.Base(from), n))
		if s.taken(to) {
			continue
		}
		if err := s.writeIntent(from, to); err != nil {
			return "", err
		}
		err := movePage(from, to)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", errors.Join(err, removeMissing(filepath.Join(s.stateDir(from), intentName)))
		}
		break
	}
	if l := s.pages[from]; l != nil {
		if err := l.Retire(to); err != nil {
			log.Printf("close the log of %s: %v", from, err)
		}
		delete(s.pages, from)
	}
	t := &s.tabs
	t.mu.Lock()
	if at, ok := t.opened[from]; ok {
		t.opened[to] = at
		delete(t.opened, from)
	}
	t.mu.Unlock()
	return to, s.finishMove(from, to)
}

func (s *Server) taken(path string) bool {
	for _, p := range []string{path, page.FilesDir(path), s.stateDir(path)} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}

func (s *Server) writeIntent(from, to string) error {
	b, err := json.Marshal(moveIntent{From: from, To: to})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.stateDir(from), intentName), b)
}

func (s *Server) finishMove(from, to string) error {
	oldDir, newDir := s.stateDir(from), s.stateDir(to)
	if _, err := os.Lstat(newDir); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(oldDir, newDir); err != nil {
			return err
		}
	}
	if err := writeAtomic(filepath.Join(newDir, "page.txt"), []byte(to+"\n")); err != nil {
		return err
	}
	if err := page.MarkSaved(newDir); err != nil {
		return err
	}
	if err := removeMissing(filepath.Join(newDir, viewingName)); err != nil {
		return err
	}
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(oldDir, movedName), []byte(to+"\n")); err != nil {
		return err
	}
	return removeMissing(filepath.Join(newDir, intentName))
}

func removeMissing(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Server) movedTo(path string) (string, bool) {
	to := path
	for range 32 {
		b, err := os.ReadFile(filepath.Join(s.stateDir(to), movedName))
		if err != nil {
			break
		}
		to = strings.TrimSuffix(string(b), "\n")
	}
	return to, to != path
}

func (s *Server) recoverMoves() {
	dirs, err := os.ReadDir(filepath.Join(s.home, "pages"))
	if err != nil {
		return
	}
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(s.home, "pages", d.Name(), intentName))
		if err != nil {
			continue
		}
		var m moveIntent
		if err := json.Unmarshal(b, &m); err != nil || !filepath.IsAbs(m.From) || !filepath.IsAbs(m.To) {
			log.Printf("move %s: unreadable %s: %s", d.Name(), intentName, b)
			continue
		}
		if err := s.recoverMove(d.Name(), m); err != nil {
			log.Printf("finish moving %s to %s: %v", m.From, m.To, err)
		}
	}
}

func (s *Server) recoverMove(id string, m moveIntent) error {
	_, fromErr := os.Lstat(m.From)
	if id == page.ID(m.From) && fromErr == nil {
		return undoMove(m.From, m.To, filepath.Join(s.home, "pages", id, intentName))
	}
	if _, err := os.Lstat(m.To); err != nil {
		return removeMissing(filepath.Join(s.home, "pages", id, intentName))
	}
	return s.finishMove(m.From, m.To)
}

func undoMove(from, to, intent string) error {
	if same, _ := sameBytes(from, to); same {
		if err := os.Remove(to); err != nil {
			return err
		}
	}
	filesFrom, filesTo := page.FilesDir(from), page.FilesDir(to)
	if _, err := os.Lstat(filesFrom); errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Lstat(filesTo); err == nil {
			if err := os.Rename(filesTo, filesFrom); err != nil {
				return err
			}
		}
	}
	return removeMissing(intent)
}

func sameBytes(a, b string) (bool, error) {
	x, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	y, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(x, y), nil
}

func movePage(from, to string) error {
	filesFrom, filesTo := page.FilesDir(from), page.FilesDir(to)
	hasFiles := false
	if fi, err := os.Stat(filesFrom); err == nil && fi.IsDir() {
		hasFiles = true
	}
	if hasFiles {
		if _, err := os.Lstat(filesTo); !errors.Is(err, fs.ErrNotExist) {
			return fs.ErrExist
		}
	}
	var err error
	if hasFiles && filepath.Base(filesFrom) != filepath.Base(filesTo) {
		err = rewriteInto(from, to)
	} else {
		err = linkOrCopy(from, to)
	}
	if err != nil {
		return err
	}
	if hasFiles {
		if err := moveDir(filesFrom, filesTo); err != nil {
			return errors.Join(err, os.Remove(to))
		}
	}
	return os.Remove(from)
}

func rewriteInto(from, to string) error {
	src, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return writeNewFile(to, page.RenameImageRefs(src, from, to), from)
}

func linkOrCopy(from, to string) error {
	err := os.Link(from, to)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return err
	}
	src, readErr := os.ReadFile(from)
	if readErr != nil {
		return errors.Join(err, readErr)
	}
	return writeNewFile(to, src, from)
}

func writeNewFile(path string, data []byte, like string) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(like); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := filepath.Join(filepath.Dir(path), ".~"+filepath.Base(path)+".saving")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err == nil {
		err = os.Link(tmp, path)
	}
	return errors.Join(err, os.Remove(tmp))
}

func moveDir(from, to string) error {
	err := os.Rename(from, to)
	if err == nil {
		return nil
	}
	if copyErr := copyDir(from, to); copyErr != nil {
		return errors.Join(err, copyErr, os.RemoveAll(to))
	}
	return os.RemoveAll(from)
}

func copyDir(from, to string) error {
	root, err := os.OpenRoot(from)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(to, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.Mkdir(dst, 0o755) //nolint:gosec // an images folder next to the page
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := root.ReadFile(rel)
		if err != nil {
			return err
		}
		return writeNewFile(dst, data, filepath.Join(from, filepath.FromSlash(rel)))
	})
}

func Tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if rel == "." {
			return "~"
		}
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}
