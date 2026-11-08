// Package notebook lists every page kapi knows, on the home page and in the sidebar of every page.
package notebook

import (
	"cmp"
	"embed"
	"io/fs"
	"net/http"
	"slices"

	"github.com/xaaha/kapi/internal/web/app"
)

//go:embed files
var files embed.FS

type Index interface {
	Index() ([]app.Page, error)
}

type Service struct{ index Index }

func New(index Index) *Service { return &Service{index: index} }

func (*Service) Name() string { return "notebook" }

func (s *Service) Routes(r app.Router) {
	r.Get("/pages", s.pages)
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
	slices.SortStableFunc(pages, func(a, b app.Page) int { return cmp.Or(b.Changed.Compare(a.Changed), cmp.Compare(a.Path, b.Path)) })
	if pages == nil {
		pages = []app.Page{}
	}
	w.Header().Set("Cache-Control", "no-store")
	app.JSON(w, http.StatusOK, pages)
}
