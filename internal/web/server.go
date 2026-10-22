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
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/render"
)

//go:embed static
var static embed.FS

// Pages finds a page by its 16-hex id; a page kapi has never seen is fs.ErrNotExist.
type Pages interface {
	Page(id string) (*page.Store, *eventlog.Log, error)
	PagePath(id string) (string, error)
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
	s.mux.HandleFunc("GET /p/{id}/events", s.withPage(s.handleEvents))
	s.mux.HandleFunc("POST /p/{id}/messages", s.withPage(s.handleMessage))
	s.mux.HandleFunc("POST /p/{id}/ops", s.withPage(s.handleOp))
	s.mux.HandleFunc("POST /p/{id}/answers", s.withPage(s.handleAnswer))
	for _, name := range []string{"kapi.js", "chrome.css"} {
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

var reopenable = regexp.MustCompile(`^p/([0-9a-f]{16})/(events)?$`)

func (s *Server) refuseToken(w http.ResponseWriter, seg, rest string) {
	m := reopenable.FindStringSubmatch(rest)
	if m == nil {
		http.Error(w, "wrong token; open the page with kapi open", http.StatusForbidden)
		return
	}
	reopen := "kapi open <page>"
	if path, err := s.pages.PagePath(m[1]); err == nil && s.issuedBefore(seg) {
		reopen = "kapi open " + path
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "kapi restarted", "reopen": reopen})
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
	return err == nil && mt == "application/json" && r.Header.Get("Origin") == "http://"+s.host
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
		if err := refresh(st, l); err != nil {
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
			st, l, err := s.pages.Page(id)
			if err == nil {
				err = refresh(st, l)
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				log.Printf("poll %s: %v", id, err)
			}
		}
	}
}

func refresh(st *page.Store, l *eventlog.Log) error {
	sum, changed, err := st.Refresh()
	if err != nil || !changed {
		return err
	}
	return appendRevision(l, "agent", sum)
}

func appendRevision(l *eventlog.Log, author string, sum page.Summary) error {
	_, err := l.Append(&eventlog.Event{Author: author, Kind: "revision", Target: strings.Join(sum.IDs, ","), Text: sum.Text})
	return err
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
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(filepath.Base(st.Path())))
	fmt.Fprintf(&b, "<style>%s</style>\n", render.CSS())
	b.WriteString("<link rel=\"stylesheet\" href=\"../../static/chrome.css\">\n</head>\n<body>\n")
	fmt.Fprintf(&b, "<main id=\"kapi-page\" data-rev=\"%d\">\n%s\n</main>\n", rev, body)
	b.WriteString("<script src=\"../../static/kapi.js\" defer></script>\n</body>\n</html>\n")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, b.String())
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request, st *page.Store, _ *eventlog.Log) {
	src, rev := st.Source()
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
	if _, err := w.Write(out); err != nil { //nolint:gosec // render output, which has no scripts
		log.Printf("write block: %v", err)
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
	for {
		events, changed := l.After(after)
		for _, e := range events {
			data, err := json.Marshal(e)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, data); err != nil {
				return
			}
			after = e.Seq
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

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request, _ *page.Store, l *eventlog.Log) {
	var in struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Text   string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Kind != "message" || strings.TrimSpace(in.Text) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": `want kind "message" and non-empty text`})
		return
	}
	e, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Target: in.Target, Text: in.Text})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"seq": e.Seq})
}

func (s *Server) handleOp(w http.ResponseWriter, r *http.Request, st *page.Store, l *eventlog.Log) {
	var in struct {
		Rev uint64  `json:"rev"`
		Op  page.Op `json:"op"`
	}
	if !decode(w, r, &in) {
		return
	}
	rev, sum, err := st.Apply(in.Rev, in.Op)
	if errors.Is(err, page.ErrStale) {
		writeJSON(w, http.StatusConflict, map[string]uint64{"rev": rev})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if sum.Text == "" {
		writeJSON(w, http.StatusOK, map[string]uint64{"rev": rev})
		return
	}
	if err := appendRevision(l, "human", sum); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]uint64{"rev": rev})
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
