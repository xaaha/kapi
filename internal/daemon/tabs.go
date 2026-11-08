package daemon

import (
	"sync"
	"time"
)

type tabs struct {
	mu      sync.Mutex
	open    int
	changed chan struct{}
	opened  map[string]time.Time
}

// Tab counts a browser tab showing the notebook until done is called.
func (s *Server) Tab() (done func()) {
	t := &s.tabs
	t.mu.Lock()
	t.open++
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.open--
			t.mu.Unlock()
		})
	}
}

// Changes is closed the next time the notebook's page list changes.
func (s *Server) Changes() <-chan struct{} {
	t := &s.tabs
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.changed == nil {
		t.changed = make(chan struct{})
	}
	return t.changed
}

func (s *Server) notebookChanged() {
	t := &s.tabs
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.changed != nil {
		close(t.changed)
		t.changed = nil
	}
}

func (s *Server) markOpened(path string) (shown bool) {
	t := &s.tabs
	t.mu.Lock()
	if t.opened == nil {
		t.opened = map[string]time.Time{}
	}
	t.opened[path] = time.Now().UTC()
	shown = t.open > 0
	t.mu.Unlock()
	s.viewingMu.Lock()
	shown = shown || len(s.viewers) > 0
	s.viewingMu.Unlock()
	s.notebookChanged()
	return shown
}

func (s *Server) openedAt(path string) time.Time {
	t := &s.tabs
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.opened[path]
}
