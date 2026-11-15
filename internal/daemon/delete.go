package daemon

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/osfile"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/trash"
	"github.com/xaaha/kapi/internal/web/app"
)

const (
	keepDeleted  = time.Hour
	deletionFile = "deletion.json"
	stateDir     = "state"
)

var restoredFiles = func(string) {}

type deletion struct {
	Path  string       `json:"path"`
	Title string       `json:"title"`
	At    time.Time    `json:"at"`
	Items []trash.Item `json:"items"`
}

func (s *Server) deletedDir(id string) string { return filepath.Join(s.home, "deleted", id) }

func (s *Server) stateDir(id string) string { return filepath.Join(s.home, "pages", id) }

type deletedError string

func (e deletedError) Error() string { return string(e) }

func (deletedError) Is(target error) bool { return target == fs.ErrNotExist }

func (s *Server) refuseDeleted(path string) error {
	if s.deleting[path] {
		return deletedError(path + " is being deleted; stop working on it")
	}
	if _, err := os.Stat(s.deletedDir(page.ID(path))); err != nil {
		return nil
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return deletedError(path + " was deleted and is in the Trash; stop working on it")
}

func (s *Server) Delete(id string) (app.Deleted, error) {
	if !pageID.MatchString(id) {
		return app.Deleted{}, fs.ErrNotExist
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	path, err := s.PagePath(id)
	if err != nil {
		return app.Deleted{}, err
	}
	title := page.Title(path)
	if at, ok := stampOf(path); ok {
		t, _ := s.preview(path, at)
		title = cmp.Or(t, title)
	}
	l, err := s.page(path)
	if err != nil {
		return app.Deleted{}, err
	}
	s.mu.Lock()
	s.deleting[path] = true
	st := s.stores[path]
	s.mu.Unlock()
	defer s.doneDeleting(path)
	if st != nil {
		if err := st.Settle(); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("settle %s: %v", path, err)
		}
	}
	aside := s.deletedDir(id)
	d := &deletion{Path: path, Title: title, At: time.Now().UTC()}
	kept := s.keepImages(path)
	if err := s.trashFiles(aside, d, trashed(path, kept)); err != nil {
		return app.Deleted{}, err
	}
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindEnd, Text: eventlog.EndDeleted}); err != nil {
		return app.Deleted{}, untrash(aside, d, err)
	}
	s.mu.Lock()
	delete(s.stores, path)
	delete(s.pages, path)
	s.mu.Unlock()
	if err := l.Close(); err != nil {
		log.Printf("close %s: %v", path, err)
	}
	if err := os.Remove(filepath.Join(s.stateDir(id), viewingName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("delete %s: %v", path, err)
	}
	if err := osfile.RenameNoReplace(s.stateDir(id), filepath.Join(aside, stateDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		err = untrash(aside, d, err)
		s.doneDeleting(path)
		s.endPlainly(path)
		return app.Deleted{}, err
	}
	s.notebookChanged()
	return app.Deleted{ID: id, Title: title, KeptImages: kept}, nil
}

func (s *Server) keepImages(path string) bool {
	if _, err := os.Lstat(page.FilesDir(path)); err != nil {
		return false
	}
	used, err := s.usedByAnotherPage(path, page.FilesDir(path))
	return used || err != nil
}

func (s *Server) doneDeleting(path string) {
	s.mu.Lock()
	delete(s.deleting, path)
	s.mu.Unlock()
}

func (s *Server) endPlainly(path string) {
	l, err := s.page(path)
	if err == nil {
		_, err = l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindEnd})
	}
	if err != nil {
		log.Printf("end %s: %v", path, err)
	}
}

func untrash(aside string, d *deletion, err error) error {
	for i := len(d.Items) - 1; i >= 0; i-- {
		err = errors.Join(err, trash.Restore(d.Items[i]))
	}
	return errors.Join(err, os.RemoveAll(aside))
}

func trashed(path string, keptImages bool) []string {
	if keptImages {
		return []string{path}
	}
	return []string{path, page.FilesDir(path)}
}

func (s *Server) trashFiles(aside string, d *deletion, paths []string) error {
	if err := os.RemoveAll(aside); err != nil {
		return err
	}
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		it, err := s.Trash.Move(p)
		if err != nil {
			return untrash(aside, d, err)
		}
		d.Items = append(d.Items, it)
	}
	b, err := json.Marshal(d)
	if err != nil {
		return untrash(aside, d, err)
	}
	if err := writeAtomic(filepath.Join(aside, deletionFile), b); err != nil {
		return untrash(aside, d, err)
	}
	return nil
}

func readDeletion(dir string) (*deletion, error) {
	b, err := os.ReadFile(filepath.Join(dir, deletionFile))
	if err != nil {
		return nil, err
	}
	var d deletion
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Server) Restore(id string) (app.Deleted, error) {
	if !pageID.MatchString(id) {
		return app.Deleted{}, fs.ErrNotExist
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	aside := s.deletedDir(id)
	d, err := readDeletion(aside)
	if err != nil {
		return app.Deleted{}, err
	}
	name := filepath.Base(d.Path)
	s.mu.Lock()
	s.deleting[d.Path] = true
	s.mu.Unlock()
	defer s.doneDeleting(d.Path)
	if _, err := os.Lstat(s.stateDir(id)); err == nil {
		return app.Deleted{}, fmt.Errorf("could not put %s back: it was opened again after it was deleted", name)
	}
	for _, it := range d.Items {
		if _, err := os.Lstat(it.Path); err == nil {
			return app.Deleted{}, fmt.Errorf("could not put %s back: %s: %w", name, it.Path, trash.ErrOccupied)
		}
	}
	for i, it := range d.Items {
		if err := trash.Restore(it); err != nil {
			if i == 0 {
				return app.Deleted{}, fmt.Errorf("could not put %s back: %w", name, err)
			}
			log.Printf("restore %s: %v", it.Path, err)
		}
	}
	restoredFiles(d.Path)
	if err := osfile.RenameNoReplace(filepath.Join(aside, stateDir), s.stateDir(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return app.Deleted{}, fmt.Errorf("put %s back, but not its notes and history: %w", name, err)
	}
	if err := os.RemoveAll(aside); err != nil {
		log.Printf("restore %s: %v", d.Path, err)
	}
	s.doneDeleting(d.Path)
	s.endPlainly(d.Path)
	s.notebookChanged()
	return app.Deleted{ID: id, Title: d.Title}, nil
}

func (s *Server) sweepDeleted(now time.Time) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	root := filepath.Join(s.home, "deleted")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		at := time.Time{}
		if d, err := readDeletion(dir); err == nil {
			at = d.At
		} else if fi, err := e.Info(); err == nil {
			at = fi.ModTime()
		}
		if now.Sub(at) < keepDeleted {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("sweep deleted %s: %v", e.Name(), err)
		}
	}
}
