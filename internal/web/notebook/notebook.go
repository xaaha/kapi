// Package notebook lists every page kapi knows, on the home page and in the sidebar of every page.
package notebook

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/xaaha/kapi/internal/web/app"
)

//go:embed files
var files embed.FS

type Index interface {
	Index() ([]app.Page, error)
	Tab() (done func())
	Changes() <-chan struct{}
}

type Service struct{ index Index }

func New(index Index) *Service { return &Service{index: index} }

func (*Service) Name() string { return "notebook" }

func (s *Service) Routes(r app.Router) {
	r.Get("/pages", s.pages)
	r.Get("/events", s.events)
}

func (*Service) Files() fs.FS {
	sub, err := fs.Sub(files, "files")
	if err != nil {
		panic(err)
	}
	return sub
}

func (s *Service) pages(w http.ResponseWriter, _ *http.Request) {
	pages, err := s.index.Index()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if pages == nil {
		pages = []app.Page{}
	}
	w.Header().Set("Cache-Control", "no-store")
	app.JSON(w, http.StatusOK, pages)
}

func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	defer s.index.Tab()()
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
		return
	}
	for {
		changed := s.index.Changes()
		if _, err := fmt.Fprint(w, "data: changed\n\n"); err != nil {
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
