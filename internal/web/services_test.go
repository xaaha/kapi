package web

import (
	"fmt"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
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
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "app" || e.Name() == "static" || e.Name() == "browsertest" {
			continue
		}
		root := os.DirFS(e.Name())
		err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(name) != ".go" {
				return err
			}
			src, err := fs.ReadFile(root, name)
			if err != nil {
				return err
			}
			path := filepath.Join(e.Name(), name)
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
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceFilesReachOnlyTheirOwnFolderAndTheCore(t *testing.T) {
	for _, svc := range append(services(nil), fakeService{}) {
		files := svc.Files()
		if files == nil {
			continue
		}
		err := fs.WalkDir(files, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			src, err := fs.ReadFile(files, name)
			if err != nil {
				return err
			}
			for _, problem := range crossings(svc.Name(), name, src) {
				t.Errorf("%s/%s: %s", svc.Name(), name, problem)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCrossingsCatchEveryWayOutOfAService(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		bad       bool
	}{
		{"main.js", `import { h } from "../../static/core.js";`, false},
		{"main.js", `import { searchDialog } from "../../static/search-dialog.js";`, false},
		{"main.js", `import { list } from "./pages.js";`, false},
		{"deep/view.js", `import { list } from "../pages.js";`, false},
		{"main.js", `export { list } from "./pages.js";`, false},
		{"main.js", `const m = await import("./lazy.js");`, false},
		{"main.js", `import "./../../other/main.js";`, true},
		{"main.js", `import { x } from "../other/main.js";`, true},
		{"main.js", `export { x } from "../../other-service/files/shared.js";`, true},
		{"main.js", `export * from "../other/shared.js";`, true},
		{"main.js", `import("../../other-service/files/main.js");`, true},
		{"main.js", "const m = await import(`./${name}.js`);", true},
		{"main.js", `const m = await import(path);`, true},
		{"main.js", `import { x } from "../../static/kapi.js";`, true},
		{"main.js", `import { x } from "/static/core.js";`, true},
		{"main.js", `import { x } from "lodash";`, true},
		{"main.js", "import {\n  h,\n} from \"../other/x.js\";", true},
		{"style.css", `@import url("home.css");`, false},
		{"style.css", `.logo { mask: url("../../static/logo.svg"); }`, false},
		{"style.css", `.a { background: url(data:image/png;base64,AAAA); }`, false},
		{"style.css", `@import "../other/style.css";`, true},
		{"style.css", `.a { background: url(../other/bg.png); }`, true},
	} {
		got := crossings("svc", tc.name, []byte(tc.src))
		if (len(got) > 0) != tc.bad {
			t.Errorf("%s %q: problems %v, want bad=%v", tc.name, tc.src, got, tc.bad)
		}
	}
}

var (
	jsFrom    = regexp.MustCompile(`\b(?:import|export)\b[^;'"` + "`" + `]*?\bfrom\s*(['"])(.*?)['"]`)
	jsBare    = regexp.MustCompile(`\bimport\s*(['"])(.*?)['"]`)
	jsDynamic = regexp.MustCompile(`\bimport\s*\(\s*([^)]*?)\s*\)`)
	jsLiteral = regexp.MustCompile(`^(['"])([^'"]*)['"]$`)
	cssURL    = regexp.MustCompile(`(?:@import\s+(['"])(.*?)['"])|url\(\s*['"]?([^'")]*?)['"]?\s*\)`)
)

func crossings(svc, name string, src []byte) []string {
	served := path.Join("assets", svc, name)
	var refs []string
	var problems []string
	switch path.Ext(name) {
	case ".js":
		for _, m := range jsFrom.FindAllSubmatch(src, -1) {
			refs = append(refs, string(m[2]))
		}
		for _, m := range jsBare.FindAllSubmatch(src, -1) {
			refs = append(refs, string(m[2]))
		}
		for _, m := range jsDynamic.FindAllSubmatch(src, -1) {
			lit := jsLiteral.FindSubmatch(m[1])
			if lit == nil {
				problems = append(problems, fmt.Sprintf("import(%s) has a computed path; use a string literal", m[1]))
				continue
			}
			refs = append(refs, string(lit[2]))
		}
	case ".css":
		for _, m := range cssURL.FindAllSubmatch(src, -1) {
			ref := string(m[2]) + string(m[3])
			if !strings.HasPrefix(ref, "data:") && !strings.HasPrefix(ref, "#") {
				refs = append(refs, ref)
			}
		}
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref, "/") || strings.Contains(ref, ":") {
			problems = append(problems, fmt.Sprintf("%q is not a relative path", ref))
			continue
		}
		if path.Ext(name) == ".js" && !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
			problems = append(problems, fmt.Sprintf("%q is a bare module name", ref))
			continue
		}
		to := path.Join(path.Dir(served), ref)
		own := strings.HasPrefix(to, "assets/"+svc+"/")
		// Explicit public core components; page controllers remain private.
		core := to == "static/core.js" || to == "static/search-dialog.js" || (path.Ext(name) == ".css" && strings.HasPrefix(to, "static/"))
		if !own && !core {
			problems = append(problems, fmt.Sprintf("%q resolves to %s, outside assets/%s/", ref, to, svc))
		}
	}
	return problems
}
