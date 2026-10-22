package daemon

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	reopenWithin = 3 * time.Hour
	reopenAtMost = 5
	viewedName   = "viewed"
)

type recentPage struct {
	id, path string
	viewed   time.Time
}

func (s *Server) Viewed(id string) {
	s.viewedMu.Lock()
	defer s.viewedMu.Unlock()
	stamp := time.Now().UTC().Format(time.RFC3339Nano) + "\n"
	if err := writeFile(filepath.Join(s.home, "pages", id, viewedName), stamp); err != nil {
		log.Printf("viewed %s: %v", id, err)
	}
}

func (s *Server) Reopened(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reopened[id]
}

func (s *Server) recentPages(now time.Time) []recentPage {
	root := filepath.Join(s.home, "pages")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []recentPage
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(root, d.Name(), viewedName))
		if err != nil {
			continue
		}
		viewed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
		if err != nil || now.Sub(viewed) > reopenWithin {
			continue
		}
		path, err := s.PagePath(d.Name())
		if err != nil {
			continue
		}
		if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, recentPage{id: d.Name(), path: path, viewed: viewed})
	}
	slices.SortFunc(out, func(a, b recentPage) int { return b.viewed.Compare(a.viewed) })
	return out[:min(len(out), reopenAtMost)]
}

func (s *Server) reopen(pages []recentPage) {
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
