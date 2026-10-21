package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/web"
)

var ErrAlreadyRunning = errors.New("daemon already running")

type Request struct {
	Op     string `json:"op"`
	Page   string `json:"page"`
	Reader string `json:"reader,omitempty"`
	Author string `json:"author,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Target string `json:"target,omitempty"`
	Text   string `json:"text,omitempty"`
	Seq    uint64 `json:"seq,omitempty"`
}

type Response struct {
	Error   string           `json:"error,omitempty"`
	Event   *eventlog.Event  `json:"event,omitempty"`
	Events  []eventlog.Event `json:"events,omitempty"`
	Readers []ReaderStatus   `json:"readers,omitempty"`
	Pages   []PageStatus     `json:"pages,omitempty"`
	URL     string           `json:"url,omitempty"`
}

type PageStatus struct {
	Path    string         `json:"path"`
	Readers []ReaderStatus `json:"readers,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type ReaderStatus struct {
	Reader   string    `json:"reader"`
	Attached bool      `json:"attached"`
	Since    time.Time `json:"since,omitzero"`
	Cursor   uint64    `json:"cursor"`
	Pending  int       `json:"pending"`
}

type Server struct {
	home string

	mu       sync.Mutex
	pages    map[string]*eventlog.Log
	stores   map[string]*page.Store
	attached map[string]map[string]time.Time
	presence map[string]map[string]string
	baseURL  string
	token    string
}

func New(home string) *Server {
	return &Server{
		home:     home,
		pages:    map[string]*eventlog.Log{},
		stores:   map[string]*page.Store{},
		attached: map[string]map[string]time.Time{},
		presence: map[string]map[string]string{},
	}
}

func HTTPURLPath(home string) string { return filepath.Join(home, "http.url") }

const maxSocketPath = 103

func SocketPath(home string) string { return filepath.Join(home, "kapi.sock") }

func CheckSocketPath(home string) error {
	if p := SocketPath(home); len(p) > maxSocketPath {
		return fmt.Errorf("socket path is %d bytes, over the %d byte unix limit; set KAPI_HOME to a shorter directory: %s", len(p), maxSocketPath, p)
	}
	return nil
}

func (s *Server) Serve(ctx context.Context) error {
	if err := CheckSocketPath(s.home); err != nil {
		return err
	}
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	sock := SocketPath(s.home)
	lock, err := os.OpenFile(filepath.Join(s.home, "kapi.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("%w on %s", ErrAlreadyRunning, sock)
		}
		return err
	}
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	stopHTTP, err := s.serveHTTP(ctx)
	if err != nil {
		ln.Close()
		return err
	}
	defer stopHTTP()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) serveHTTP(ctx context.Context) (func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, err
	}
	host := ln.Addr().String()
	s.mu.Lock()
	s.baseURL, s.token = "http://"+host, hex.EncodeToString(b)
	s.mu.Unlock()
	h := web.New(s, s.token, host)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: %v", err)
		}
	}()
	pollCtx, stopPoll := context.WithCancel(ctx)
	go h.Poll(pollCtx, 300*time.Millisecond)
	urlFile := HTTPURLPath(s.home)
	if err := writeFile(urlFile, s.baseURL+"/?t="+s.token+"\n"); err != nil {
		stopPoll()
		srv.Close()
		return nil, err
	}
	return func() {
		stopPoll()
		srv.Close()
		os.Remove(urlFile)
	}, nil
}

func writeFile(path, text string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Server) page(path string) (*eventlog.Log, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("page path must be absolute: %q", path)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.pages[path]; ok {
		return l, nil
	}
	dir := filepath.Join(s.home, "pages", page.ID(path))
	l, err := eventlog.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "page.txt"), []byte(path+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := clearPresence(l); err != nil {
		return nil, err
	}
	s.pages[path] = l
	return l, nil
}

func clearPresence(l *eventlog.Log) error {
	events, _ := l.After(0)
	last := map[string]string{}
	var readers []string
	for _, e := range events {
		if e.Kind == eventlog.KindPresence {
			if _, ok := last[e.Author]; !ok {
				readers = append(readers, e.Author)
			}
			last[e.Author] = e.Text
		}
	}
	for _, r := range readers {
		if last[r] == "none" {
			continue
		}
		if _, err := l.Append(&eventlog.Event{Author: r, Kind: eventlog.KindPresence, Text: "none"}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) store(path string) (*page.Store, *eventlog.Log, error) {
	l, err := s.page(path)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[path]; ok {
		return st, l, nil
	}
	st, err := page.Open(s.home, path)
	if err != nil {
		return nil, nil, err
	}
	s.stores[path] = st
	return st, l, nil
}

// Page finds a page kapi has seen by its 16-hex id, for the browser.
func (s *Server) Page(id string) (*page.Store, *eventlog.Log, error) {
	b, err := os.ReadFile(filepath.Join(s.home, "pages", id, "page.txt"))
	if err != nil {
		return nil, nil, err
	}
	return s.store(strings.TrimSuffix(string(b), "\n"))
}

func (s *Server) setPresence(path, reader, state string, l *eventlog.Log) {
	s.mu.Lock()
	if s.presence[path] == nil {
		s.presence[path] = map[string]string{}
	}
	prev := s.presence[path][reader]
	if prev == state || (prev == "" && state == "none") {
		s.mu.Unlock()
		return
	}
	s.presence[path][reader] = state
	s.mu.Unlock()
	if _, err := l.Append(&eventlog.Event{Author: reader, Kind: eventlog.KindPresence, Text: state}); err != nil {
		log.Printf("presence %s %s: %v", reader, state, err)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)
	var req Request
	if err := dec.Decode(&req); err != nil {
		if !errors.Is(err, io.EOF) {
			reply(enc, &Response{Error: err.Error()})
		}
		return
	}
	if req.Op == "open" {
		s.open(enc, req.Page)
		return
	}
	if req.Op == "pages" {
		pages, err := s.listPages()
		if err != nil {
			reply(enc, &Response{Error: err.Error()})
			return
		}
		reply(enc, &Response{Pages: pages})
		return
	}
	l, err := s.page(req.Page)
	if err != nil {
		reply(enc, &Response{Error: err.Error()})
		return
	}
	switch req.Op {
	case "append":
		e, err := l.Append(&eventlog.Event{Author: req.Author, Kind: req.Kind, Target: req.Target, Text: req.Text})
		if err != nil {
			reply(enc, &Response{Error: err.Error()})
			return
		}
		if e.Kind == "message" && s.presenceOf(req.Page, e.Author) == "busy" {
			s.setPresence(req.Page, e.Author, "watching", l)
		}
		reply(enc, &Response{Event: &e})
	case "pending":
		reply(enc, &Response{Events: l.Pending(req.Reader)})
	case "ack":
		if err := l.Ack(req.Reader, req.Seq); err != nil {
			reply(enc, &Response{Error: err.Error()})
			return
		}
		reply(enc, &Response{})
	case "status":
		reply(enc, &Response{Readers: s.status(req.Page, l)})
	case "watch":
		s.watch(dec, enc, &req, l)
	default:
		reply(enc, &Response{Error: fmt.Sprintf("unknown op %q", req.Op)})
	}
}

func (s *Server) open(enc *json.Encoder, path string) {
	if _, _, err := s.store(path); err != nil {
		reply(enc, &Response{Error: err.Error()})
		return
	}
	s.mu.Lock()
	url := s.baseURL + "/p/" + page.ID(path) + "/?t=" + s.token
	s.mu.Unlock()
	reply(enc, &Response{URL: url})
}

func (s *Server) presenceOf(path, reader string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.presence[path][reader]
}

func reply(enc *json.Encoder, resp *Response) {
	if err := enc.Encode(resp); err != nil {
		log.Printf("reply: %v", err)
	}
}

func (s *Server) watch(dec *json.Decoder, enc *json.Encoder, req *Request, l *eventlog.Log) {
	if req.Reader == "" {
		reply(enc, &Response{Error: "watch needs a reader"})
		return
	}
	s.setAttached(req.Page, req.Reader, true)
	defer s.setAttached(req.Page, req.Reader, false)
	s.setPresence(req.Page, req.Reader, "watching", l)
	defer s.setPresence(req.Page, req.Reader, "none", l)

	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for {
			var a Request
			if err := dec.Decode(&a); err != nil {
				return
			}
			if err := l.Ack(req.Reader, a.Seq); err != nil {
				log.Printf("ack %s %d: %v", req.Reader, a.Seq, err)
			}
		}
	}()

	sent := l.Cursor(req.Reader)
	for {
		events, changed := l.After(sent)
		for _, e := range events {
			sent = e.Seq
			if e.Kind == eventlog.KindPresence {
				continue
			}
			if err := enc.Encode(Response{Event: &e}); err != nil {
				return
			}
			if e.Author == "human" && (e.Kind == "message" || e.Kind == "answer") {
				s.setPresence(req.Page, req.Reader, "busy", l)
			}
		}
		select {
		case <-changed:
		case <-gone:
			return
		}
	}
}

func (s *Server) setAttached(page, reader string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attached[page] == nil {
		s.attached[page] = map[string]time.Time{}
	}
	if on {
		s.attached[page][reader] = time.Now().UTC()
	} else {
		delete(s.attached[page], reader)
	}
}

func (s *Server) status(page string, l *eventlog.Log) []ReaderStatus {
	s.mu.Lock()
	attached := maps.Clone(s.attached[page])
	s.mu.Unlock()
	readers := map[string]bool{"agent": true}
	for r := range attached {
		readers[r] = true
	}
	var out []ReaderStatus
	for r := range readers {
		since, on := attached[r]
		out = append(out, ReaderStatus{Reader: r, Attached: on, Since: since, Cursor: l.Cursor(r), Pending: len(l.Pending(r))})
	}
	return out
}

func (s *Server) listPages() ([]PageStatus, error) {
	root := filepath.Join(s.home, "pages")
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []PageStatus
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(root, d.Name())
		b, err := os.ReadFile(filepath.Join(dir, "page.txt"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			out = append(out, PageStatus{Path: dir, Error: err.Error()})
			continue
		}
		out = append(out, s.pageStatus(strings.TrimSuffix(string(b), "\n"), dir))
	}
	slices.SortFunc(out, func(a, b PageStatus) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func (s *Server) pageStatus(path, dir string) PageStatus {
	s.mu.Lock()
	l, open := s.pages[path]
	attached := maps.Clone(s.attached[path])
	s.mu.Unlock()
	if open {
		readers := s.status(path, l)
		slices.SortFunc(readers, func(a, b ReaderStatus) int { return strings.Compare(a.Reader, b.Reader) })
		return PageStatus{Path: path, Readers: readers}
	}
	cursors, pending, err := eventlog.Summary(dir)
	if err != nil {
		return PageStatus{Path: path, Error: err.Error()}
	}
	names := map[string]bool{"agent": true}
	for r := range cursors {
		names[r] = true
	}
	for r := range attached {
		names[r] = true
	}
	var readers []ReaderStatus
	for r := range names {
		since, on := attached[r]
		readers = append(readers, ReaderStatus{Reader: r, Attached: on, Since: since, Cursor: cursors[r], Pending: pending(r)})
	}
	slices.SortFunc(readers, func(a, b ReaderStatus) int { return strings.Compare(a.Reader, b.Reader) })
	return PageStatus{Path: path, Readers: readers}
}
