package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xaaha/kapi/internal/attach"
	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/export"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/publish"
	"github.com/xaaha/kapi/internal/render"
)

//go:embed static
var static embed.FS

// Pages finds a page by its 16-hex id; a page kapi has never seen is fs.ErrNotExist.
type Pages interface {
	Page(id string) (*page.Store, *eventlog.Log, error)
	PagePath(id string) (string, error)
	Viewing(id string) (done func())
	Reopened(id string) bool
	Attachments() *attach.Store
}

type Server struct {
	pages    Pages
	token    string
	host     string
	previous [][32]byte
	mux      *http.ServeMux

	mu   sync.Mutex
	seen map[string]bool
}

// New serves pages to requests addressed to host (127.0.0.1:<port>) whose path starts with /<token>/.
// previous holds the SHA-256 of tokens earlier runs issued; only those learn a page's path.
func New(pages Pages, token, host string, previous [][32]byte) *Server {
	s := &Server{pages: pages, token: token, host: host, previous: previous, mux: http.NewServeMux(), seen: map[string]bool{}}
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "kapi is running. Open a page with: kapi open <page>")
	})
	s.mux.HandleFunc("GET /p/{id}/{$}", s.withPage(s.handlePage))
	s.mux.HandleFunc("GET /p/{id}/blocks/{block}", s.withPage(s.handleBlock))
	s.mux.HandleFunc("GET /p/{id}/source/{block}", s.withPage(s.handleSource))
	s.mux.HandleFunc("GET /p/{id}/export/html", s.withPage(s.handleExportHTML))
	s.mux.HandleFunc("GET /p/{id}/events", s.withPage(s.handleEvents))
	s.mux.HandleFunc("POST /p/{id}/messages", s.withPage(s.handleMessage))
	s.mux.HandleFunc("POST /p/{id}/notes", s.withPage(s.handleNotes))
	s.mux.HandleFunc("POST /p/{id}/resolve", s.withPage(s.handleResolve))
	s.mux.HandleFunc("POST /p/{id}/delete-notes", s.withPage(s.handleDeleteNotes))
	s.mux.HandleFunc("POST /p/{id}/end", s.withPage(s.handleEnd))
	s.mux.HandleFunc("POST /p/{id}/files", s.withPage(s.handleUpload))
	s.mux.HandleFunc("GET /p/{id}/files/{name}", s.withPage(s.handleFile))
	s.mux.HandleFunc("POST /p/{id}/images", s.withPage(s.handleImageUpload))
	s.mux.HandleFunc("GET /p/{id}/{dir}/{name}", s.withPage(s.handlePageImage))
	s.mux.HandleFunc("GET /p/{id}/export/markdown", s.withPage(s.handleMarkdown))
	s.mux.HandleFunc("POST /p/{id}/ops", s.withPage(s.handleOp))
	s.mux.HandleFunc("POST /p/{id}/answers", s.withPage(s.handleAnswer))
	for _, name := range []string{"theme.js", "kapi.js", "chrome.css", "logo.svg", "favicon.svg",
		"fonts/hanken-grotesk.woff2", "fonts/hanken-grotesk-italic.woff2", "fonts/source-serif-4.woff2", "fonts/source-serif-4-italic.woff2"} {
		s.mux.HandleFunc("GET /static/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, static, "static/"+name)
		})
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if r.Host != s.host {
		http.Error(w, "wrong host", http.StatusForbidden)
		return
	}
	if !tidy(r.URL.Path) {
		http.Error(w, "untidy path", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodPost && !s.sameOriginJSON(r) {
		http.Error(w, "posts must come from this page as application/json", http.StatusForbidden)
		return
	}
	seg, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !s.validToken(seg) {
		s.refuseToken(w, seg, rest)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path, r2.URL.RawPath = "/"+rest, ""
	s.mux.ServeHTTP(w, r2)
}

var reopenable = regexp.MustCompile(`^p/([0-9a-f]{16})/(events|messages|notes|resolve|delete-notes|end|files|images|ops|answers|export/html|export/markdown)?$`)

func (s *Server) refuseToken(w http.ResponseWriter, seg, rest string) {
	m := reopenable.FindStringSubmatch(rest)
	if m == nil {
		http.Error(w, "wrong token; open the page with kapi open", http.StatusForbidden)
		return
	}
	body := map[string]any{"error": "kapi restarted", "reopen": "kapi open <page>"}
	if s.issuedBefore(seg) {
		if path, err := s.pages.PagePath(m[1]); err == nil {
			body["reopen"] = "kapi open " + path
		}
		if s.pages.Reopened(m[1]) {
			body["reopened"] = true
		}
	}
	writeJSON(w, http.StatusForbidden, body)
}

func (s *Server) issuedBefore(seg string) bool {
	h := sha256.Sum256([]byte(seg))
	known := false
	for _, p := range s.previous {
		known = subtle.ConstantTimeCompare(h[:], p[:]) == 1 || known
	}
	return known
}

func tidy(p string) bool {
	clean := path.Clean(p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean == p
}

func (s *Server) sameOriginJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	upload := strings.HasPrefix(mt, "image/") && (strings.HasSuffix(r.URL.Path, "/files") || strings.HasSuffix(r.URL.Path, "/images"))
	return err == nil && (mt == "application/json" || upload) && r.Header.Get("Origin") == "http://"+s.host
}

func (s *Server) validToken(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

var pageID = regexp.MustCompile(`^[0-9a-f]{16}$`)

type pageHandler func(w http.ResponseWriter, r *http.Request, st *page.Store, l *eventlog.Log)

func (s *Server) withPage(h pageHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !pageID.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		st, l, err := s.pages.Page(id)
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.seen[id] = true
		s.mu.Unlock()
		if _, _, err := st.Refresh(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h(w, r, st, l)
	}
}

// Poll picks up edits to the files of pages the browser has asked for, every interval, until ctx ends.
func (s *Server) Poll(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		ids := make([]string, 0, len(s.seen))
		for id := range s.seen {
			ids = append(ids, id)
		}
		s.mu.Unlock()
		for _, id := range ids {
			st, _, err := s.pages.Page(id)
			if err == nil {
				_, _, err = st.Refresh()
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				log.Printf("poll %s: %v", id, err)
			}
		}
	}
}

func pageTitle(path string) string {
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	if strings.EqualFold(ext, ".html") || strings.EqualFold(ext, ".htm") {
		if title := strings.TrimSuffix(name, ext); title != "" {
			return title
		}
	}
	return name
}

func (s *Server) handlePage(w http.ResponseWriter, _ *http.Request, st *page.Store, _ *eventlog.Log) {
	src, rev := st.Source()
	body, err := render.Render(src)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(pageTitle(st.Path())))
	b.WriteString("<link rel=\"icon\" type=\"image/svg+xml\" href=\"../../static/favicon.svg\">\n")
	b.WriteString("<script src=\"../../static/theme.js\"></script>\n")
	fmt.Fprintf(&b, "<style>%s</style>\n", render.CSS())
	b.WriteString("<link rel=\"stylesheet\" href=\"../../static/chrome.css\">\n</head>\n<body>\n")
	fmt.Fprintf(&b, "<main id=\"kapi-page\" data-rev=\"%d\" data-undo=\"%t\" data-path=\"%s\">\n%s\n</main>\n", rev, st.CanUndo(), html.EscapeString(st.Path()), body)
	b.WriteString("<script src=\"../../static/kapi.js\" defer></script>\n</body>\n</html>\n")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, b.String())
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request, st *page.Store, _ *eventlog.Log) {
	src, rev := st.Source()
	if q := r.URL.Query().Get("rev"); q != "" {
		n, err := strconv.ParseUint(q, 10, 64)
		if err != nil {
			http.Error(w, "rev must be a revision number", http.StatusBadRequest)
			return
		}
		if src, err = st.SourceAt(n); err != nil {
			if errors.Is(err, page.ErrNoRev) {
				http.NotFound(w, r)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		rev = n
	}
	id := r.PathValue("block")
	if _, err := page.Outer(src, id); err != nil {
		http.NotFound(w, r)
		return
	}
	out, err := render.Block(src, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Kapi-Rev", strconv.FormatUint(rev, 10))
	w.Header().Set("Kapi-Undo", strconv.FormatBool(st.CanUndo()))
	if _, err := w.Write(out); err != nil { //nolint:gosec // render output, which has no scripts
		log.Printf("write block: %v", err)
	}
}

func (s *Server) handleExportHTML(w http.ResponseWriter, _ *http.Request, st *page.Store, _ *eventlog.Log) {
	src, _ := st.Source()
	name := filepath.Base(st.Path())
	doc, err := publish.Export(pageTitle(name), src, page.FilesDir(st.Path()))
	if errors.Is(err, publish.ErrMissingImages) {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if _, err := w.Write(doc); err != nil { //nolint:gosec // a download of the render output, which has no scripts
		log.Printf("write export: %v", err)
	}
}

func (s *Server) handleSource(w http.ResponseWriter, r *http.Request, st *page.Store, _ *eventlog.Log) {
	part := page.Outer
	switch r.URL.Query().Get("part") {
	case "":
	case "inner":
		part = page.Inner
	default:
		http.Error(w, `part must be "inner" or left out`, http.StatusBadRequest)
		return
	}
	src, rev := st.Source()
	out, err := part(src, r.PathValue("block"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Kapi-Rev", strconv.FormatUint(rev, 10))
	if _, err := w.Write(out); err != nil { //nolint:gosec // text/plain with nosniff
		log.Printf("write source: %v", err)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, _ *page.Store, l *eventlog.Log) {
	var after uint64
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		n, err := strconv.ParseUint(last, 10, 64)
		if err != nil {
			http.Error(w, "Last-Event-ID must be an event number", http.StatusBadRequest)
			return
		}
		after = n
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := fmt.Fprint(w, "retry: 1000\n\n"); err != nil {
		return
	}
	sent := map[[2]string]uint64{}
	defer s.pages.Viewing(r.PathValue("id"))()
	for {
		events, changed := l.After(after)
		for i := range events {
			e := &events[i]
			data, err := json.Marshal(e)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, data); err != nil { //nolint:gosec // text/event-stream of JSON, not HTML
				return
			}
			after = e.Seq
		}
		seen, queued, acked := l.Cursors()
		for _, positions := range []struct {
			kind string
			seqs map[string]uint64
		}{{"queued", queued}, {"read", seen}} {
			for _, reader := range slices.Sorted(maps.Keys(positions.seqs)) {
				rr := readReceipt{Kind: positions.kind, Reader: reader, Seq: positions.seqs[reader]}
				if sent[[2]string{rr.Kind, reader}] == rr.Seq || eventlog.CheckReader(reader) != nil {
					continue
				}
				sent[[2]string{rr.Kind, reader}] = rr.Seq
				data, err := json.Marshal(rr)
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
					return
				}
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
		select {
		case <-changed:
		case <-acked:
		case <-r.Context().Done():
			return
		}
	}
}

type readReceipt struct {
	Kind   string `json:"kind"`
	Reader string `json:"reader"`
	Seq    uint64 `json:"seq"`
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request, _ *page.Store, l *eventlog.Log) {
	var in struct {
		Kind   string   `json:"kind"`
		Target string   `json:"target"`
		Text   string   `json:"text"`
		Files  []string `json:"files"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Kind != "message" || (strings.TrimSpace(in.Text) == "" && len(in.Files) == 0) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": `want kind "message" and non-empty text or an image`})
		return
	}
	files, ok := s.useFiles(w, r, [][]string{in.Files})
	if !ok {
		return
	}
	e, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Target: in.Target, Files: files[0], Text: in.Text})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"seq": e.Seq})
}

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request, st *page.Store, l *eventlog.Log) {
	var in struct {
		Notes []struct {
			Target string   `json:"target"`
			Part   string   `json:"part"`
			Quote  string   `json:"quote"`
			Prefix string   `json:"prefix"`
			Suffix string   `json:"suffix"`
			Re     []uint64 `json:"re"`
			Files  []string `json:"files"`
			Text   string   `json:"text"`
		} `json:"notes"`
		Rev   uint64   `json:"rev"`
		Text  string   `json:"text"`
		Files []string `json:"files"`
	}
	if !decode(w, r, &in) {
		return
	}
	rev := in.Rev
	if _, cur := st.Source(); rev == 0 || rev > cur {
		rev = cur
	}
	events := make([]eventlog.Event, 0, len(in.Notes)+1)
	for i := range in.Notes {
		n := &in.Notes[i]
		if (strings.TrimSpace(n.Text) == "" && len(n.Files) == 0) || n.Target == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "every note needs a target and text or an image"})
			return
		}
		if err := l.CheckNotes(n.Re); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		events = append(events, eventlog.Event{Author: "human", Kind: eventlog.KindNote, Target: n.Target, Part: n.Part,
			Quote: n.Quote, Prefix: n.Prefix, Suffix: n.Suffix, Rev: rev, Re: n.Re, Text: n.Text})
	}
	groups := make([][]string, 0, len(in.Notes)+1)
	for i := range in.Notes {
		groups = append(groups, in.Notes[i].Files)
	}
	message := strings.TrimSpace(in.Text) != "" || len(in.Files) > 0
	if message {
		groups = append(groups, in.Files)
	}
	files, ok := s.useFiles(w, r, groups)
	if !ok {
		return
	}
	for i := range in.Notes {
		events[i].Files = files[i]
	}
	if message {
		events = append(events, eventlog.Event{Author: "human", Kind: "message", Files: files[len(files)-1], Text: in.Text})
	}
	if len(events) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "want at least one note or a message"})
		return
	}
	out, err := l.AppendAll(events)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"seq": out[len(out)-1].Seq})
}

func (s *Server) useFiles(w http.ResponseWriter, r *http.Request, groups [][]string) ([][]string, bool) {
	var all []string
	unique := make([][]string, len(groups))
	for i, names := range groups {
		for _, n := range names {
			if !slices.Contains(unique[i], n) {
				unique[i] = append(unique[i], n)
			}
		}
		names = unique[i]
		if len(names) > attach.MaxPerSend {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("at most %d images per note or message", attach.MaxPerSend)})
			return nil, false
		}
		all = append(all, names...)
	}
	out := make([][]string, len(groups))
	if len(all) == 0 {
		return out, true
	}
	paths, err := s.pages.Attachments().Use(r.PathValue("id"), all)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return nil, false
	}
	for i, names := range unique {
		for _, name := range names {
			out[i] = append(out[i], paths[name])
		}
	}
	return out, true
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, _ *page.Store, _ *eventlog.Log) {
	s.upload(w, r, func(data []byte) (attach.File, error) { return s.pages.Attachments().Put(r.PathValue("id"), data) })
}

func (s *Server) handleImageUpload(w http.ResponseWriter, r *http.Request, _ *page.Store, _ *eventlog.Log) {
	path, err := s.pages.PagePath(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.upload(w, r, func(data []byte) (attach.File, error) { return attach.Save(page.FilesDir(path), data) })
}

func (s *Server) handlePageImage(w http.ResponseWriter, r *http.Request, _ *page.Store, _ *eventlog.Log) {
	path, err := s.pages.PagePath(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name, ok := page.ImageName(page.FilesDir(path), "./"+url.PathEscape(r.PathValue("dir"))+"/"+r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, filepath.Join(page.FilesDir(path), name))
}

func (s *Server) handleMarkdown(w http.ResponseWriter, r *http.Request, st *page.Store, _ *eventlog.Log) {
	src, _ := st.Source()
	if id := r.URL.Query().Get("block"); id != "" {
		block, err := page.Outer(src, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		md, err := render.Markdown(block, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(md); err != nil {
			log.Printf("write block markdown: %v", err)
		}
		return
	}
	f, err := export.Markdown(filepath.Base(st.Path()), src, page.FilesDir(st.Path()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", f.Type)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(f.Data); err != nil {
		log.Printf("write export: %v", err)
	}
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request, save func([]byte) (attach.File, error)) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, attach.MaxBytes))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": attach.ErrTooBig.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the image: " + err.Error()})
		return
	}
	f, err := save(data)
	switch {
	case errors.Is(err, attach.ErrType):
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": err.Error()})
	case errors.Is(err, attach.ErrTooBig):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
	case errors.Is(err, attach.ErrFull):
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		writeJSON(w, http.StatusOK, f)
	}
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request, _ *page.Store, _ *eventlog.Log) {
	path, err := s.pages.Attachments().Path(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, path)
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request, _ *page.Store, l *eventlog.Log) {
	var in struct {
		Note uint64 `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := l.CheckNotes([]uint64{in.Note}); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	e, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindResolve, Re: []uint64{in.Note}})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"seq": e.Seq})
}

func (s *Server) handleDeleteNotes(w http.ResponseWriter, r *http.Request, _ *page.Store, l *eventlog.Log) {
	var in struct {
		Notes []uint64 `json:"notes"`
	}
	if !decode(w, r, &in) {
		return
	}
	slices.Sort(in.Notes)
	in.Notes = slices.Compact(in.Notes)
	if len(in.Notes) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "want at least one note to delete"})
		return
	}
	if err := l.CheckNotes(in.Notes); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	e, err := l.Append(&eventlog.Event{Author: "human", Kind: eventlog.KindDeleteNotes, Re: in.Notes})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"seq": e.Seq})
}

func (s *Server) handleEnd(w http.ResponseWriter, r *http.Request, _ *page.Store, l *eventlog.Log) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	e, err := l.End("human")
	if errors.Is(err, eventlog.ErrEnded) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"seq": e.Seq})
}

type opResult struct {
	Rev  uint64   `json:"rev"`
	Undo bool     `json:"undo"`
	Seq  uint64   `json:"seq,omitempty"`
	IDs  []string `json:"ids,omitempty"`
}

func (s *Server) handleOp(w http.ResponseWriter, r *http.Request, st *page.Store, _ *eventlog.Log) {
	var in struct {
		Rev uint64  `json:"rev"`
		Op  page.Op `json:"op"`
	}
	if !decode(w, r, &in) {
		return
	}
	rev, sum, err := st.Apply(in.Rev, &in.Op)
	if errors.Is(err, page.ErrStale) {
		writeJSON(w, http.StatusConflict, opResult{Rev: rev, Undo: st.CanUndo()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, opResult{Rev: rev, Undo: st.CanUndo(), Seq: sum.Seq, IDs: sum.IDs})
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request, st *page.Store, l *eventlog.Log) {
	var in struct {
		Form   string              `json:"form"`
		Values map[string][]string `json:"values"`
	}
	if !decode(w, r, &in) {
		return
	}
	src, _ := st.Source()
	formID, err := page.FormID(src, in.Form)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	text, err := json.Marshal(in)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	e, err := l.Append(&eventlog.Event{Author: "human", Kind: "answer", Target: formID, Text: string(text)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"seq": e.Seq})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body: " + err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
