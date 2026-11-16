package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/web/app"
)

const missingCheck = time.Second

func (s *Server) serveMissing(w http.ResponseWriter, r *http.Request, m *app.Missing) {
	switch r.Pattern {
	case "GET /p/{id}/{$}":
		data, err := json.Marshal(m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var main strings.Builder
		fmt.Fprintf(&main, "<main id=\"kapi-page\" data-rev=\"0\" data-undo=\"false\" data-saved=\"false\" data-path=\"%s\" data-missing=\"%s\"></main>\n", html.EscapeString(m.Path), html.EscapeString(string(data)))
		s.shell(w, r, "../../", m.Title, main.String())
	case "GET /p/{id}/events":
		s.missingEvents(w, r, m)
	default:
		app.JSON(w, http.StatusGone, map[string]any{"error": "the page's file is missing; restore it to keep working", "missing": m})
	}
}

func sendMissing(w io.Writer, m *app.Missing) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: missing\ndata: %s\n\n", data)
	return err
}

func (s *Server) missingEvents(w http.ResponseWriter, r *http.Request, m *app.Missing) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := fmt.Fprint(w, "retry: 1000\n\n"); err != nil {
		return
	}
	if sendMissing(w, m) != nil || rc.Flush() != nil {
		return
	}
	check := time.NewTicker(missingCheck)
	defer check.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-check.C:
		}
		if _, gone := s.pages.Missing(m.ID); gone {
			continue
		}
		back := struct {
			Rev      uint64 `json:"rev"`
			Restored bool   `json:"restored"`
		}{}
		if st, _, err := s.pages.Page(m.ID); err == nil {
			_, _, err = st.Refresh()
			_, back.Rev = st.Source()
			back.Restored = err == nil && m.Rev > 0 && back.Rev == m.Rev
		}
		data, err := json.Marshal(back)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "event: present\ndata: %s\n\n", data); err == nil { //nolint:gosec // text/event-stream of JSON, not HTML
			_ = rc.Flush()
		}
		return
	}
}

func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !pageID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	m, err := s.pages.Recover(id)
	switch {
	case errors.Is(err, fs.ErrExist):
		app.JSON(w, http.StatusConflict, map[string]string{"error": "the file is back already, so kapi left it as it is"})
	case errors.Is(err, page.ErrNoCopy):
		app.JSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, fs.ErrNotExist):
		app.JSON(w, http.StatusNotFound, map[string]string{"error": "kapi has no such page to restore"})
	case err != nil:
		app.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		app.JSON(w, http.StatusOK, m)
	}
}
