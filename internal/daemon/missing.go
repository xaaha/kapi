package daemon

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/web/app"
)

// Missing reports a page kapi knows whose file is gone, which is not a page that moved or is being deleted.
func (s *Server) Missing(id string) (app.Missing, bool) {
	if !pageID.MatchString(id) {
		return app.Missing{}, false
	}
	path, err := s.PagePath(id)
	if err != nil {
		return app.Missing{}, false
	}
	m, _, gone := s.missing(path)
	return m, gone
}

func (s *Server) missing(path string) (app.Missing, page.Copy, bool) {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return app.Missing{}, page.Copy{}, false
	}
	s.mu.Lock()
	deleting := s.deleting[path]
	s.mu.Unlock()
	if deleting {
		return app.Missing{}, page.Copy{}, false
	}
	m := app.Missing{ID: page.ID(path), Path: path, Title: page.Title(path)}
	c, err := page.LatestCopy(s.home, path)
	if err != nil || c.Rev == 0 {
		return m, page.Copy{}, true
	}
	m.Rev, m.At = c.Rev, c.At
	if at, ok := stampOf(c.File); ok {
		title, _ := s.preview(c.File, at)
		m.Title = cmp.Or(title, m.Title)
	}
	return m, c, true
}

// Recover writes the newest version kapi kept back to a missing page's file.
func (s *Server) Recover(id string) (app.Missing, error) {
	if !pageID.MatchString(id) {
		return app.Missing{}, fs.ErrNotExist
	}
	path, err := s.PagePath(id)
	if err != nil {
		return app.Missing{}, err
	}
	return s.recoverPath(path)
}

func (s *Server) recoverPath(path string) (app.Missing, error) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	err := s.refuseDeleted(path)
	s.mu.Unlock()
	if err != nil {
		return app.Missing{}, err
	}
	if _, err := os.Stat(filepath.Join(s.home, "pages", page.ID(path), "page.txt")); err != nil {
		return app.Missing{}, err
	}
	c, err := page.Recover(s.home, path)
	if err != nil {
		return app.Missing{}, err
	}
	s.notebookChanged()
	m := app.Missing{ID: page.ID(path), Path: path, Title: page.Title(path), Rev: c.Rev, At: c.At}
	if at, ok := stampOf(path); ok {
		title, _ := s.preview(path, at)
		m.Title = cmp.Or(title, m.Title)
	}
	return m, nil
}

func (s *Server) recoverOp(path string) *Response {
	if !filepath.IsAbs(path) {
		return &Response{Error: fmt.Sprintf("page path must be absolute: %q", path)}
	}
	if m, _, gone := s.missing(path); !gone || m.Rev == 0 {
		return &Response{}
	}
	m, err := s.recoverPath(path)
	if errors.Is(err, fs.ErrExist) || errors.Is(err, page.ErrNoCopy) {
		return &Response{}
	}
	if err != nil {
		return &Response{Error: err.Error()}
	}
	return &Response{Notice: fmt.Sprintf("%s was missing; restored version %d from %s", path, m.Rev, m.At.Local().Format("2 Jan 15:04"))}
}
