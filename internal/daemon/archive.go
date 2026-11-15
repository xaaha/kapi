package daemon

import (
	"context"
	"log"
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
	mu       sync.Mutex
	sweep    sync.Mutex
	started  time.Time
	released map[string]time.Time
}

func (s *Server) startRetention() {
	r := &s.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = s.now().UTC()
}

func (s *Server) inGrace(now time.Time) bool {
	r := &s.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.started) < archiveGrace
}

func (s *Server) release(id string) {
	r := &s.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released == nil {
		r.released = map[string]time.Time{}
	}
	r.released[id] = s.now().UTC()
}

func (s *Server) releasedAt(id string) time.Time {
	r := &s.retention
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.released[id]
}

func (s *Server) inUse(id, path string) bool {
	s.viewingMu.Lock()
	viewed := s.viewers[id] > 0
	s.viewingMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return viewed || len(s.attached[path]) > 0
}

func (s *Server) policy() settings.Policy {
	prefs, err := s.prefs.Get(context.Background())
	if err != nil {
		log.Printf("retention: %v", err)
		return settings.DefaultRetention().Policy()
	}
	return prefs.Retention.Policy()
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
		id := page.ID(path)
		if s.inUse(id, path) {
			return time.Time{}
		}
		if at := s.releasedAt(id); at.After(last) {
			last = at
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
	p := s.policy()
	now := s.now().UTC()
	archiving := !p.KeepWhileOpen || !s.inGrace(now)
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
		if !ready {
			continue
		}
		archived, err := s.markArchived(d, now, lc.Touched)
		if err != nil {
			log.Printf("archive %s: %v", d.path, err)
			continue
		}
		changed = changed || archived
	}
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

func (s *Server) markArchived(d pageDir, at, touched time.Time) (bool, error) {
	s.mu.Lock()
	st, open := s.stores[d.path]
	if !open {
		defer s.mu.Unlock()
		return page.MarkArchivedAt(d.dir, at, touched)
	}
	s.mu.Unlock()
	return st.MarkArchived(at, touched)
}
