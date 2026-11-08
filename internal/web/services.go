package web

import (
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/xaaha/kapi/internal/render"
	"github.com/xaaha/kapi/internal/web/app"
	"github.com/xaaha/kapi/internal/web/notebook"
)

func services(pages Pages) []app.Service {
	return []app.Service{
		notebook.New(pages),
	}
}

func (s *Server) mount(list []app.Service) {
	core, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("GET /static/{file...}", files(core))
	for _, svc := range list {
		svc.Routes(app.NewRouter(s.mux, svc.Name()))
		if f := svc.Files(); f != nil {
			s.mux.Handle("GET /assets/"+svc.Name()+"/{file...}", files(f))
		}
	}
	s.services = list
}

func files(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, name)
	})
}

func (s *Server) handleHome(w http.ResponseWriter, _ *http.Request) {
	s.shell(w, "", "kapi", "<main id=\"kapi-home\"></main>\n")
}

// shell writes the HTML document around main. root is the relative path from the request to the token root.
func (s *Server) shell(w http.ResponseWriter, root, title, main string) {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(title))
	fmt.Fprintf(&b, "<link rel=\"icon\" type=\"image/svg+xml\" href=\"%sstatic/favicon.svg\">\n", root)
	fmt.Fprintf(&b, "<script src=\"%sstatic/theme.js\"></script>\n", root)
	fmt.Fprintf(&b, "<style>%s</style>\n", render.CSS())
	fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"%sstatic/chrome.css\">\n", root)
	fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"%sstatic/tables.css\">\n", root)
	for _, svc := range s.services {
		if ships(svc, "style.css") {
			fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"%sassets/%s/style.css\">\n", root, svc.Name())
		}
	}
	b.WriteString("</head>\n<body>\n")
	b.WriteString(main)
	fmt.Fprintf(&b, "<script src=\"%sstatic/shortcuts.js\" defer></script>\n", root)
	fmt.Fprintf(&b, "<script src=\"%sstatic/tables.js\" defer></script>\n", root)
	fmt.Fprintf(&b, "<script src=\"%sstatic/kapi.js\" defer></script>\n", root)
	for _, svc := range s.services {
		if ships(svc, "main.js") {
			fmt.Fprintf(&b, "<script type=\"module\" src=\"%sassets/%s/main.js\"></script>\n", root, svc.Name())
		}
	}
	b.WriteString("</body>\n</html>\n")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, b.String())
}

func ships(svc app.Service, name string) bool {
	f := svc.Files()
	if f == nil {
		return false
	}
	_, err := fs.Stat(f, name)
	return err == nil
}
