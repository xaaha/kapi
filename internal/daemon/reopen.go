package daemon

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

const (
	reopenAtMost = 5
	viewingName  = "viewing"
)

var pageID = regexp.MustCompile(`^[0-9a-f]{16}$`)

type viewedPage struct {
	id, path string
	since    time.Time
}

func (s *Server) Viewing(id string) (done func()) {
	file := filepath.Join(s.home, "pages", id, viewingName)
	s.viewingMu.Lock()
	defer s.viewingMu.Unlock()
	s.viewers[id]++
	if s.viewers[id] == 1 {
		if err := writeFile(file, ""); err != nil {
			log.Printf("viewing %s: %v", id, err)
		}
	}
	return func() {
		s.viewingMu.Lock()
		defer s.viewingMu.Unlock()
		if s.viewers[id]--; s.viewers[id] > 0 {
			return
		}
		delete(s.viewers, id)
		select {
		case <-s.stopping:
			return
		default:
		}
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("viewing %s: %v", id, err)
		}
	}
}

func (s *Server) Reopened(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reopened[id]
}

func (s *Server) takeViewedPages() []viewedPage {
	root := filepath.Join(s.home, "pages")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []viewedPage
	for _, d := range dirs {
		if !pageID.MatchString(d.Name()) {
			continue
		}
		file := filepath.Join(root, d.Name(), viewingName)
		fi, err := os.Stat(file)
		if err != nil {
			continue
		}
		if err := os.Remove(file); err != nil {
			log.Printf("viewing %s: %v", d.Name(), err)
		}
		if d.Name() == s.ReopenExcept {
			continue
		}
		path, err := s.PagePath(d.Name())
		if err != nil {
			continue
		}
		if pfi, err := os.Stat(path); err != nil || !pfi.Mode().IsRegular() {
			continue
		}
		out = append(out, viewedPage{id: d.Name(), path: path, since: fi.ModTime()})
	}
	slices.SortFunc(out, func(a, b viewedPage) int { return b.since.Compare(a.since) })
	return out[:min(len(out), reopenAtMost)]
}

func (s *Server) reopen(pages []viewedPage) {
	s.mu.Lock()
	base := s.baseURL
	s.mu.Unlock()
	for _, p := range pages {
		if err := s.Browser(base + "p/" + p.id + "/"); err != nil {
			s.mu.Lock()
			delete(s.reopened, p.id)
			s.mu.Unlock()
			log.Printf("reopen %s: %v", p.path, err)
			continue
		}
		log.Printf("reopened %s in the browser", p.path)
	}
}
