package daemon

import (
	"cmp"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/osfile"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/settings"
)

const redirectsWithoutDeletion = 30 * 24 * time.Hour

func (s *Server) deletingAllowed(now time.Time) bool {
	r := &s.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.started) >= archiveEvery
}

func expired(lc *page.Lifecycle, p settings.Policy, now time.Time) bool {
	due := deletesAt(lc, p)
	return !lc.Saved && !due.IsZero() && !now.Before(due)
}

func (s *Server) ownedByKapi(path string) bool {
	f, err := s.folders()
	if err != nil {
		return false
	}
	return inside(resolved(f.Root), resolved(path))
}

func (s *Server) expire(d pageDir, want *page.Lifecycle, seen stamp, p settings.Policy) (bool, error) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	id := filepath.Base(d.dir)
	owned := s.ownedByKapi(d.path)
	t, _ := s.preview(d.path, seen)
	title := cmp.Or(t, page.Title(d.path))
	l, err := s.page(d.path)
	if err != nil {
		return false, err
	}
	claimed, err := s.claimExpired(d, want, seen)
	if !claimed || err != nil {
		return false, err
	}
	defer s.doneDeleting(d.path)
	aside := s.deletedDir(id)
	rec := &deletion{Path: d.path, Title: title, At: s.now().UTC()}
	var files []string
	end := eventlog.EndForgotten
	kept := false
	if owned {
		kept = s.keepImages(d.path)
		files, end = trashed(d.path, kept), eventlog.EndDeleted
	}
	if err := s.trashFiles(aside, rec, files); err != nil {
		s.forgetStore(d.path)
		return false, err
	}
	if _, err := l.Append(&eventlog.Event{Author: "kapi", Kind: eventlog.KindEnd, Text: end}); err != nil {
		s.forgetStore(d.path)
		return false, untrash(aside, rec, err)
	}
	s.mu.Lock()
	delete(s.stores, d.path)
	delete(s.pages, d.path)
	s.mu.Unlock()
	if err := l.Close(); err != nil {
		log.Printf("close %s: %v", d.path, err)
	}
	if err := os.Remove(filepath.Join(d.dir, viewingName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("delete %s: %v", d.path, err)
	}
	if err := osfile.RenameNoReplace(d.dir, filepath.Join(aside, stateDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		err = untrash(aside, rec, err)
		s.doneDeleting(d.path)
		s.endPlainly(d.path)
		return false, err
	}
	why := "archived " + want.Archived.Format(time.RFC3339) + ", retention.deleteAfter " + p.DeleteAfter.String() + " passed"
	switch {
	case !owned:
		log.Printf("retention: forgot %s (%s); the file is outside the library folder, so it stays where it is", d.path, why)
	case kept:
		log.Printf("retention: moved %s to the Trash (%s); kept %s, another page uses it", d.path, why, page.FilesDir(d.path))
	default:
		log.Printf("retention: moved %s and its images folder to the Trash (%s)", d.path, why)
	}
	return true, nil
}

func (s *Server) claimExpired(d pageDir, want *page.Lifecycle, seen stamp) (bool, error) {
	s.viewingMu.Lock()
	defer s.viewingMu.Unlock()
	s.mu.Lock()
	if s.viewers[filepath.Base(d.dir)] > 0 || len(s.attached[d.path]) > 0 || s.deleting[d.path] {
		s.mu.Unlock()
		return false, nil
	}
	if at, ok := stampOf(d.path); !ok || at != seen {
		s.mu.Unlock()
		return false, nil
	}
	st, open := s.stores[d.path]
	if !open {
		defer s.mu.Unlock()
		lc, err := page.ReadLifecycle(d.dir)
		if err != nil || lc.Saved || lc.Archived.IsZero() || !lc.Archived.Equal(want.Archived) || !lc.Touched.Equal(want.Touched) {
			return false, err
		}
		s.deleting[d.path] = true
		return true, nil
	}
	s.deleting[d.path] = true
	s.mu.Unlock()
	ok, err := st.Expire(want.Archived, want.Touched)
	if !ok || err != nil {
		s.doneDeleting(d.path)
	}
	return ok, err
}

func (s *Server) forgetStore(path string) {
	s.mu.Lock()
	delete(s.stores, path)
	s.mu.Unlock()
}

func (s *Server) sweepRedirects(now time.Time, p settings.Policy) {
	keep := cmp.Or(p.DeleteAfter, redirectsWithoutDeletion)
	root := filepath.Join(s.home, "pages")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, d := range dirs {
		if !d.IsDir() || !pageID.MatchString(d.Name()) {
			continue
		}
		s.removeRedirect(filepath.Join(root, d.Name()), now, keep)
	}
}

func (s *Server) removeRedirect(dir string, now time.Time, keep time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != movedName {
		return
	}
	fi, err := entries[0].Info()
	if err != nil || now.Sub(fi.ModTime()) < keep {
		return
	}
	to, _ := os.ReadFile(filepath.Join(dir, movedName))
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("retention: remove the redirect %s: %v", dir, err)
		return
	}
	log.Printf("retention: removed the redirect %s to %s, older than %s", dir, strings.TrimSpace(string(to)), keep)
}
