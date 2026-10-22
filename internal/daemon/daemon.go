package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"regexp"
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
	Notice  string           `json:"notice,omitempty"`
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

	Browser      func(url string) error
	ReopenExcept string

	mu       sync.Mutex
	pages    map[string]*eventlog.Log
	stores   map[string]*page.Store
	attached map[string]map[string]time.Time
	baseURL  string
	reopened map[string]bool
	stopping <-chan struct{}

	viewingMu sync.Mutex
	viewers   map[string]int

	presenceMu sync.Mutex
	presence   map[string]map[string]string
	watchers   map[string]map[string]int
}

func New(home string) *Server {
	return &Server{
		home:     home,
		pages:    map[string]*eventlog.Log{},
		stores:   map[string]*page.Store{},
		attached: map[string]map[string]time.Time{},
		reopened: map[string]bool{},
		viewers:  map[string]int{},
		presence: map[string]map[string]string{},
		watchers: map[string]map[string]int{},
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
	s.stopping = ctx.Done()
	recent := s.takeViewedPages()
	if s.Browser == nil {
		recent = nil
	}
	s.mu.Lock()
	for _, p := range recent {
		s.reopened[p.id] = true
	}
	s.mu.Unlock()
	stopHTTP, err := s.serveHTTP(ctx)
	if err != nil {
		return err
	}
	defer stopHTTP()
	var reopening sync.WaitGroup
	defer reopening.Wait()
	reopening.Go(func() { s.reopen(recent) })
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
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

var savedURL = regexp.MustCompile(`^http://(127\.0\.0\.1:\d+)/[0-9a-f]{32}/\n$`)

func (s *Server) listenHTTP() (net.Listener, string, error) {
	addr := "127.0.0.1:0"
	if b, err := os.ReadFile(HTTPURLPath(s.home)); err == nil {
		if m := savedURL.FindSubmatch(b); m != nil {
			addr = string(m[1])
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil && addr != "127.0.0.1:0" {
		log.Printf("http: %s is taken (%v); moving to a new port", addr, err)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, "", err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, "", err
	}
	return ln, hex.EncodeToString(b), nil
}

func (s *Server) serveHTTP(ctx context.Context) (func(), error) {
	ln, token, err := s.listenHTTP()
	if err != nil {
		return nil, err
	}
	host := ln.Addr().String()
	previous, err := rememberToken(s.home, token)
	if err != nil {
		ln.Close()
		return nil, err
	}
	s.mu.Lock()
	s.baseURL = "http://" + host + "/" + token + "/"
	s.mu.Unlock()
	h := web.New(s, token, host, previous)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: %v", err)
		}
	}()
	pollCtx, stopPoll := context.WithCancel(ctx)
	go h.Poll(pollCtx, 300*time.Millisecond)
	urlFile := HTTPURLPath(s.home)
	if err := writeFile(urlFile, s.baseURL+"\n"); err != nil {
		stopPoll()
		srv.Close()
		return nil, err
	}
	return func() {
		stopPoll()
		srv.Close()
	}, nil
}

const keptTokens = 3

// rememberToken returns the hashes of the tokens earlier runs issued and adds this run's.
func rememberToken(home, token string) ([][32]byte, error) {
	path := filepath.Join(home, "previous-tokens")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var previous [][32]byte
	for line := range strings.Lines(string(b)) {
		h, err := hex.DecodeString(strings.TrimSpace(line))
		if err == nil && len(h) == 32 {
			previous = append(previous, [32]byte(h))
		}
	}
	kept := slices.Concat(previous, [][32]byte{sha256.Sum256([]byte(token))})
	kept = kept[max(0, len(kept)-keptTokens):]
	var out strings.Builder
	for _, h := range kept {
		out.WriteString(hex.EncodeToString(h[:]) + "\n")
	}
	return previous, writeFile(path, out.String())
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
	st, l, _, err := s.openStore(path)
	return st, l, err
}

func (s *Server) openStore(path string) (*page.Store, *eventlog.Log, bool, error) {
	l, err := s.page(path)
	if err != nil {
		return nil, nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.stores[path]; ok {
		return st, l, false, nil
	}
	st, err := page.Open(s.home, path)
	if err != nil {
		return nil, nil, false, err
	}
	if text := st.Conversion(); text != "" {
		if _, err := l.Append(&eventlog.Event{Author: "kapi", Kind: "revision", Text: text}); err != nil {
			return nil, nil, false, err
		}
	}
	s.stores[path] = st
	return st, l, true, nil
}

// Page finds a page kapi has seen by its 16-hex id, for the browser.
func (s *Server) Page(id string) (*page.Store, *eventlog.Log, error) {
	path, err := s.PagePath(id)
	if err != nil {
		return nil, nil, err
	}
	return s.store(path)
}

func (s *Server) PagePath(id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(s.home, "pages", id, "page.txt"))
	return strings.TrimSuffix(string(b), "\n"), err
}

func (s *Server) attach(path, reader string, l *eventlog.Log) {
	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()
	if s.watchers[path] == nil {
		s.watchers[path] = map[string]int{}
	}
	s.watchers[path][reader]++
	if s.watchers[path][reader] == 1 {
		s.setAttached(path, reader, true)
		s.setPresenceLocked(path, reader, "watching", l)
	}
}

func (s *Server) detach(path, reader string, l *eventlog.Log) {
	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()
	s.watchers[path][reader]--
	if s.watchers[path][reader] == 0 {
		delete(s.watchers[path], reader)
		s.setAttached(path, reader, false)
		s.setPresenceLocked(path, reader, "none", l)
	}
}

func (s *Server) setPresenceLocked(path, reader, state string, l *eventlog.Log) {
	if s.presence[path] == nil {
		s.presence[path] = map[string]string{}
	}
	prev := s.presence[path][reader]
	if prev == state || (prev == "" && state == "none") {
		return
	}
	s.presence[path][reader] = state
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
	case "pending", "ack", "queue", "seen", "watch", "wait":
		if err := eventlog.CheckReader(req.Reader); err != nil {
			reply(enc, &Response{Error: err.Error()})
			return
		}
	}
	switch req.Op {
	case "append":
		e, err := l.Append(&eventlog.Event{Author: req.Author, Kind: req.Kind, Target: req.Target, Text: req.Text})
		if err != nil {
			reply(enc, &Response{Error: err.Error()})
			return
		}
		if e.Author != "" && eventlog.CheckReader(e.Author) == nil {
			if err := l.Ack(e.Author, l.Queued(e.Author)); err != nil {
				reply(enc, &Response{Error: err.Error()})
				return
			}
		}
		reply(enc, &Response{Event: &e})
	case "pending":
		events, through, _ := l.Unseen(req.Reader)
		if err := l.Ack(req.Reader, through); err != nil {
			reply(enc, &Response{Error: err.Error()})
			return
		}
		reply(enc, &Response{Events: events})
	case "seen":
		seq := l.Queued(req.Reader)
		if err := l.Ack(req.Reader, seq); err != nil {
			reply(enc, &Response{Error: err.Error()})
			return
		}
		reply(enc, &Response{Event: &eventlog.Event{Seq: seq}})
	case "ack":
		reply(enc, ackReply(l.Ack(req.Reader, req.Seq)))
	case "queue":
		reply(enc, ackReply(l.Queue(req.Reader, req.Seq)))
	case "status":
		reply(enc, &Response{Readers: s.status(req.Page, l)})
	case "watch":
		s.watch(dec, enc, &req, l)
	case "wait":
		s.wait(dec, enc, &req, l)
	default:
		reply(enc, &Response{Error: fmt.Sprintf("unknown op %q", req.Op)})
	}
}

func (s *Server) open(enc *json.Encoder, path string) {
	st, _, opened, err := s.openStore(path)
	if err != nil {
		reply(enc, &Response{Error: err.Error()})
		return
	}
	s.mu.Lock()
	resp := &Response{URL: s.baseURL + "p/" + page.ID(path) + "/"}
	s.mu.Unlock()
	if opened {
		resp.Notice = st.Conversion()
	}
	reply(enc, resp)
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
	s.attach(req.Page, req.Reader, l)
	defer s.detach(req.Page, req.Reader, l)

	ack := func(seq uint64) {
		if err := l.Queue(req.Reader, seq); err != nil {
			log.Printf("queue %s %d: %v", req.Reader, seq, err)
		}
	}
	var mu sync.Mutex
	sent := l.Cursor(req.Reader)
	delivered, acked, ownAfter := sent, sent, uint64(0)

	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for {
			var a Request
			if err := dec.Decode(&a); err != nil {
				return
			}
			mu.Lock()
			acked = max(acked, a.Seq)
			through := a.Seq
			if a.Seq >= delivered {
				through = max(through, ownAfter)
			}
			mu.Unlock()
			ack(through)
		}
	}()

	for {
		events, changed := l.After(sent)
		for _, e := range events {
			sent = e.Seq
			if e.Author == req.Reader {
				mu.Lock()
				caughtUp := acked >= delivered
				if !caughtUp {
					ownAfter = e.Seq
				}
				mu.Unlock()
				if caughtUp {
					ack(e.Seq)
				}
				continue
			}
			if e.Kind == eventlog.KindPresence {
				continue
			}
			mu.Lock()
			delivered = e.Seq
			mu.Unlock()
			if err := enc.Encode(Response{Event: &e}); err != nil {
				return
			}
		}
		select {
		case <-changed:
		case <-gone:
			return
		}
	}
}

func ackReply(err error) *Response {
	if err != nil {
		return &Response{Error: err.Error()}
	}
	return &Response{}
}

func (s *Server) wait(dec *json.Decoder, enc *json.Encoder, req *Request, l *eventlog.Log) {
	s.attach(req.Page, req.Reader, l)
	defer s.detach(req.Page, req.Reader, l)
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		var discard json.RawMessage
		for {
			if dec.Decode(&discard) != nil {
				return
			}
		}
	}()
	for {
		_, changed := l.After(^uint64(0))
		events, _, queued := l.Unseen(req.Reader)
		if len(events) > 0 && events[len(events)-1].Seq > queued {
			reply(enc, &Response{Events: events})
			return
		}
		select {
		case <-changed:
		case <-gone:
			return
		case <-s.stopping:
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
