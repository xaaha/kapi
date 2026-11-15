package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/settings"
)

const (
	archiveEvery = 15 * time.Minute
	archiveGrace = 5 * time.Minute
	absorbSlack  = time.Second
)

type retention struct {
	mu      sync.Mutex
	sweep   sync.Mutex
	started time.Time
	broken  bool
}

func (s *Server) startRetention() {
	r := &s.retention
	r.mu.Lock()
	r.started = s.now().UTC()
	r.mu.Unlock()
	s.releaseAttachedAtStop()
}

func (s *Server) inGrace(now time.Time) bool {
	r := &s.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.started) < archiveGrace
}

func (s *Server) releaseAttachedAtStop() {
	root := filepath.Join(s.home, "pages")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, d := range dirs {
		file := filepath.Join(root, d.Name(), attachedName)
		if _, err := os.Stat(file); err != nil || !pageID.MatchString(d.Name()) {
			continue
		}
		if err := os.Remove(file); err != nil {
			log.Printf("attached %s: %v", d.Name(), err)
		}
		s.release(d.Name())
	}
}

func (s *Server) release(id string) {
	path, err := s.PagePath(id)
	if err != nil {
		return
	}
	at := s.now().UTC()
	s.mu.Lock()
	st, open := s.stores[path]
	if !open {
		err = page.MarkReleasedAt(filepath.Join(s.home, "pages", id), at)
		s.mu.Unlock()
	} else {
		s.mu.Unlock()
		err = st.MarkReleased(at)
	}
	if err != nil {
		log.Printf("release %s: %v", path, err)
	}
}

func (s *Server) inUse(id, path string) bool {
	s.viewingMu.Lock()
	defer s.viewingMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewers[id] > 0 || len(s.attached[path]) > 0
}

func (s *Server) policy() (settings.Policy, bool) {
	prefs, err := s.prefs.Get(context.Background())
	r := &s.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		if !r.broken {
			log.Printf("retention: not archiving until settings can be read: %v", err)
		}
		r.broken = true
		return settings.Policy{}, false
	}
	r.broken = false
	return prefs.Retention.Policy(), true
}

func lifecycleOf(dir string, mod time.Time) (lc page.Lifecycle, stale bool) {
	lc, _ = page.ReadLifecycle(dir)
	mod = mod.UTC()
	stale = lc.Touched.IsZero() || mod.After(lc.Touched.Add(absorbSlack))
	if lc.Touched.IsZero() {
		lc.Touched = mod
	}
	if lc.Viewed.IsZero() {
		lc.Viewed = lc.Touched
	}
	return lc, stale
}

func (s *Server) archivesAt(path string, lc *page.Lifecycle, p settings.Policy) time.Time {
	if p.ArchiveAfter == 0 || lc.Saved || !lc.Archived.IsZero() {
		return time.Time{}
	}
	last := lc.Touched
	if p.KeepWhileOpen {
		if s.inUse(page.ID(path), path) {
			return time.Time{}
		}
		if lc.Released.After(last) {
			last = lc.Released
		}
	}
	return last.Add(p.ArchiveAfter)
}

func deletesAt(lc *page.Lifecycle, p settings.Policy) time.Time {
	if lc.Archived.IsZero() || p.DeleteAfter == 0 {
		return time.Time{}
	}
	return lc.Archived.Add(p.DeleteAfter)
}

func (s *Server) archiveLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.sweepArchive()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) sweepArchive() {
	s.retention.sweep.Lock()
	defer s.retention.sweep.Unlock()
	dirs, err := s.pageDirs()
	if err != nil {
		log.Printf("archive: %v", err)
		return
	}
	p, ok := s.policy()
	if !ok {
		return
	}
	now := s.now().UTC()
	archiving := !p.KeepWhileOpen || !s.inGrace(now)
	deleting := s.deletingAllowed(now)
	changed := false
	for _, d := range dirs {
		if d.err != nil {
			continue
		}
		at, ok := stampOf(d.path)
		if !ok {
			continue
		}
		lc, stale := lifecycleOf(d.dir, at.mod)
		due := s.archivesAt(d.path, &lc, p)
		ready := archiving && !due.IsZero() && !now.Before(due)
		if stale && (ready || !lc.Archived.IsZero()) {
			before := lc
			if lc, err = s.absorb(d.path); err != nil {
				log.Printf("archive %s: %v", d.path, err)
				continue
			}
			changed = changed || lc != before
			due = s.archivesAt(d.path, &lc, p)
			ready = archiving && !due.IsZero() && !now.Before(due)
		}
		if ready {
			archived, err := s.markArchived(d, now, lc.Touched, p.KeepWhileOpen)
			if err != nil {
				log.Printf("archive %s: %v", d.path, err)
				continue
			}
			changed = changed || archived
			continue
		}
		if !deleting || !expired(&lc, p, now) {
			continue
		}
		if at, ok = stampOf(d.path); !ok {
			continue
		}
		removed, err := s.expire(d, &lc, at, p)
		if err != nil {
			log.Printf("retention: delete %s: %v", d.path, err)
			continue
		}
		changed = changed || removed
	}
	s.sweepRedirects(now, p)
	if changed {
		s.notebookChanged()
	}
}

func (s *Server) absorb(path string) (page.Lifecycle, error) {
	st, _, err := s.store(path)
	if err != nil {
		return page.Lifecycle{}, err
	}
	if _, _, err := st.Refresh(); err != nil {
		return page.Lifecycle{}, err
	}
	return st.Lifecycle(), nil
}

func (s *Server) markArchived(d pageDir, at, touched time.Time, keepWhileOpen bool) (bool, error) {
	s.viewingMu.Lock()
	defer s.viewingMu.Unlock()
	s.mu.Lock()
	if keepWhileOpen && (s.viewers[filepath.Base(d.dir)] > 0 || len(s.attached[d.path]) > 0) {
		s.mu.Unlock()
		return false, nil
	}
	st, open := s.stores[d.path]
	if !open {
		defer s.mu.Unlock()
		return page.MarkArchivedAt(d.dir, at, touched)
	}
	s.mu.Unlock()
	return st.MarkArchived(at, touched)
}
