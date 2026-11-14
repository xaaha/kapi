package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/xaaha/kapi/internal/render"
	"github.com/xaaha/kapi/internal/settings"
	"github.com/xaaha/kapi/internal/web/app"
)

type feed struct {
	mu      sync.Mutex
	changed chan struct{}
}

func newFeed() feed { return feed{changed: make(chan struct{})} }

func (f *feed) next() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.changed
}

func (f *feed) notify() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.changed)
	f.changed = make(chan struct{})
}

func (s *Server) currentSettings(ctx context.Context) settings.Settings {
	prefs, err := s.settings.Get(ctx)
	if err != nil {
		log.Printf("settings: %v", err)
		return settings.Default()
	}
	return prefs
}

func handlePalettes(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write(render.PaletteCSS()); err != nil {
		log.Printf("write palettes: %v", err)
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	prefs, err := s.settings.Get(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	app.JSON(w, http.StatusOK, prefs)
}

func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var p settings.Patch
	if !app.Decode(w, r, &p) {
		return
	}
	prefs, err := s.settings.Update(r.Context(), p)
	if errors.Is(err, settings.ErrInvalid) {
		app.JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.feed.notify()
	app.JSON(w, http.StatusOK, prefs)
}

func (s *Server) handleSettingsEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
		return
	}
	for {
		changed := s.feed.next()
		prefs, err := s.settings.Get(r.Context())
		if err != nil {
			log.Printf("settings: %v", err)
			return
		}
		b, err := json.Marshal(prefs)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return
		}
		if err := rc.Flush(); err != nil {
			return
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		}
	}
}
