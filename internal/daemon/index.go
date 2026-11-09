package daemon

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/web/app"
)

type stamp struct {
	size int64
	mod  time.Time
}

func stampOf(path string) (stamp, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return stamp{}, false
	}
	return stamp{fi.Size(), fi.ModTime()}, true
}

type preview struct {
	at             stamp
	title, summary string
}

type session struct {
	events, positions stamp
	ended, waiting    bool
}

type indexCache struct {
	mu       sync.Mutex
	previews map[string]preview
	sessions map[string]session
}

// Index lists every page kapi has opened, for the notebook.
func (s *Server) Index() ([]app.Page, error) {
	dirs, err := s.pageDirs()
	if err != nil {
		return nil, err
	}
	out := make([]app.Page, 0, len(dirs))
	for _, d := range dirs {
		if d.err != nil {
			continue
		}
		at, ok := stampOf(d.path)
		if !ok {
			continue
		}
		p := app.Page{ID: filepath.Base(d.dir), Path: d.path, Title: page.Title(d.path), Folders: s.openers(d.path), State: s.state(d.path, d.dir), Opened: s.openedAt(d.path), Changed: at.mod}
		title, summary := s.preview(d.path, at)
		p.Title, p.Summary = cmp.Or(title, p.Title), summary
		if at, ok := stampOf(filepath.Join(d.dir, "events.ndjson")); ok && at.mod.After(p.Changed) {
			p.Changed = at.mod
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b app.Page) int {
		if d := cmp.Compare(stateRank(a.State), stateRank(b.State)); d != 0 {
			return d
		}
		if d := b.Changed.Compare(a.Changed); d != 0 {
			return d
		}
		return cmp.Compare(a.Title, b.Title)
	})
	return out, nil
}

func stateRank(state string) int {
	switch state {
	case app.Live:
		return 0
	case app.Waiting:
		return 1
	case app.Idle:
		return 2
	case app.Ended:
		return 3
	default:
		return 4
	}
}

func (s *Server) preview(path string, at stamp) (title, summary string) {
	c := &s.index
	c.mu.Lock()
	cached, ok := c.previews[path]
	c.mu.Unlock()
	if ok && cached.at == at {
		return cached.title, cached.summary
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	title, summary = page.Preview(src)
	c.mu.Lock()
	if c.previews == nil {
		c.previews = map[string]preview{}
	}
	c.previews[path] = preview{at: at, title: title, summary: summary}
	c.mu.Unlock()
	return title, summary
}

func (s *Server) state(path, dir string) string {
	s.mu.Lock()
	l, open := s.pages[path]
	live := len(s.attached[path]) > 0
	s.mu.Unlock()
	var ended, waiting bool
	if open {
		ended, waiting = l.Ended() > 0, l.Waiting()
	} else {
		c := s.closedSession(dir)
		ended, waiting = c.ended, c.waiting
	}
	switch {
	case ended:
		return app.Ended
	case live:
		return app.Live
	case waiting:
		return app.Waiting
	}
	return app.Idle
}

func (s *Server) closedSession(dir string) session {
	events, _ := stampOf(filepath.Join(dir, "events.ndjson"))
	positions, _ := stampOf(filepath.Join(dir, "positions.json"))
	c := &s.index
	c.mu.Lock()
	cached, ok := c.sessions[dir]
	c.mu.Unlock()
	if ok && cached.events == events && cached.positions == positions {
		return cached
	}
	_, _, endedSeq, err := eventlog.Summary(dir)
	if err != nil {
		return session{}
	}
	waiting, _ := eventlog.WaitingIn(dir)
	fresh := session{events: events, positions: positions, ended: endedSeq > 0, waiting: waiting}
	c.mu.Lock()
	if c.sessions == nil {
		c.sessions = map[string]session{}
	}
	c.sessions[dir] = fresh
	c.mu.Unlock()
	return fresh
}
