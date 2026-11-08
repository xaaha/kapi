package web

import (
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/xaaha/kapi/internal/web/app"
)

type fakeService struct{}

func (fakeService) Name() string { return "fake" }

func (fakeService) Routes(r app.Router) {
	r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
		app.JSON(w, http.StatusOK, map[string]string{"hello": "world"})
	})
}

func (fakeService) Files() fs.FS {
	return fstest.MapFS{"main.js": {Data: []byte("export {};\n")}, "style.css": {Data: []byte("body {}\n")}}
}

func TestAServiceGetsItsRoutesAssetsAndAPlaceInThePageShell(t *testing.T) {
	s := newSite(t, `<p data-kapi-id="aaaaaaaa">a</p>`)
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = newServer(s.pages, token, srv.Listener.Addr().String(), nil, []app.Service{fakeService{}})
	srv.Start()
	t.Cleanup(srv.Close)
	s.srv = srv
	page := s.get("/p/" + s.id + "/").body
	for _, want := range []string{
		`<script type="module" src="../../assets/fake/main.js"></script>`,
		`<link rel="stylesheet" href="../../assets/fake/style.css">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page shell lacks %q", want)
		}
	}
	if resp := s.get("/api/fake/hello"); resp.code != http.StatusOK || !strings.Contains(resp.body, `"hello":"world"`) {
		t.Errorf("GET /api/fake/hello: %d %q", resp.code, resp.body)
	}
	for _, f := range []string{"main.js", "style.css"} {
		if resp := s.get("/assets/fake/" + f); resp.code != http.StatusOK || resp.header.Get("Cache-Control") != "no-cache" {
			t.Errorf("GET fake/%s: %d %q", f, resp.code, resp.header.Get("Cache-Control"))
		}
	}
	for _, p := range []string{"/static/", "/static/fonts", "/static/fonts/", "/static/nope.js", "/assets/fake/", "/assets/nope/main.js", "/api/fake/nope"} {
		if resp := s.get(p); resp.code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", p, resp.code)
		}
	}
}

func TestServicesImportOnlyTheCore(t *testing.T) {
	const self = "github.com/xaaha/kapi/internal/web"
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	jsImport := regexp.MustCompile(`(?m)^\s*import\b[^"']*["']([^"']+)["']`)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "app" || e.Name() == "static" || e.Name() == "browsertest" {
			continue
		}
		svc := e.Name()
		root := os.DirFS(svc)
		err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			path := filepath.Join(svc, name)
			switch filepath.Ext(name) {
			case ".go":
				src, err := fs.ReadFile(root, name)
				if err != nil {
					return err
				}
				f, err := parser.ParseFile(gotoken.NewFileSet(), path, src, parser.ImportsOnly)
				if err != nil {
					return err
				}
				for _, imp := range f.Imports {
					p, _ := strconv.Unquote(imp.Path.Value)
					if (p == self || strings.HasPrefix(p, self+"/")) && p != self+"/app" {
						t.Errorf("%s imports %s; a service may import only %s/app", path, p, self)
					}
				}
			case ".js":
				src, err := fs.ReadFile(root, name)
				if err != nil {
					return err
				}
				for _, m := range jsImport.FindAllStringSubmatch(string(src), -1) {
					if m[1] != "../../static/core.js" && !strings.HasPrefix(m[1], "./") {
						t.Errorf("%s imports %q; a service script may import only ../../static/core.js and its own files", path, m[1])
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
