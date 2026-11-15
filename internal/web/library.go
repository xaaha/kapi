package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/settings"
	"github.com/xaaha/kapi/internal/web/app"
)

func (s *Server) libraryRoutes() {
	s.mux.HandleFunc("GET /library", s.handleLibrary)
	s.mux.HandleFunc("POST /p/{id}/save", s.withPage(s.handleSave))
	s.mux.HandleFunc("POST /p/{id}/unsave", s.withPage(s.handleUnsave))
}

func (s *Server) redirectMoved(w http.ResponseWriter, r *http.Request, id, to string) {
	rest := strings.TrimPrefix(r.URL.Path, "/p/"+id)
	target := "/" + s.token + "/p/" + page.ID(to) + rest
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect) //nolint:gosec // same origin: this token, a hex page id, and the request's own tidy path
}

func (s *Server) handleLibrary(w http.ResponseWriter, _ *http.Request) {
	root, folders, err := s.pages.Library()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if folders == nil {
		folders = []string{}
	}
	w.Header().Set("Cache-Control", "no-store")
	app.JSON(w, http.StatusOK, map[string]any{"library": root, "folders": folders})
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request, st *page.Store, _ *eventlog.Log) {
	var in struct {
		Folder string `json:"folder"`
	}
	if !app.Decode(w, r, &in) {
		return
	}
	to, err := s.pages.Save(st.Path(), in.Folder)
	if errors.Is(err, settings.ErrBadFolder) {
		app.JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		app.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	app.JSON(w, http.StatusOK, map[string]string{"id": page.ID(to), "path": to})
}

func (s *Server) handleUnsave(w http.ResponseWriter, r *http.Request, st *page.Store, _ *eventlog.Log) {
	var in struct{}
	if !app.Decode(w, r, &in) {
		return
	}
	if err := s.pages.Unsave(st.Path()); err != nil {
		app.JSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	app.JSON(w, http.StatusOK, map[string]string{"path": st.Path()})
}
