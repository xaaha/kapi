package daemon

import (
	"context"
	"errors"
	"io/fs"
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

func (s *Server) folders() (settings.Folders, error) {
	prefs, err := s.prefs.Get(context.Background())
	if err != nil {
		return settings.Folders{}, err
	}
	return prefs.Folders()
}

func (s *Server) pageState(path string) string {
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

func removeMissing(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Server) movedTo(path string) (string, bool) {
	to := path
	for range 32 {
		b, err := os.ReadFile(filepath.Join(s.pageState(to), movedName))
		if err != nil {
			break
		}
		to = strings.TrimSuffix(string(b), "\n")
	}
	return to, to != path
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
