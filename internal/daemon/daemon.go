package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
)

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
	attached map[string]map[string]time.Time
}

func New(home string) *Server {
	return &Server{home: home, pages: map[string]*eventlog.Log{}, attached: map[string]map[string]time.Time{}}
}

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
	if conn, err := net.Dial("unix", sock); err == nil {
		conn.Close()
		return fmt.Errorf("daemon already running on %s", sock)
	}
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

func (s *Server) page(path string) (*eventlog.Log, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("page path must be absolute: %q", path)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.pages[path]; ok {
		return l, nil
	}
	sum := sha256.Sum256([]byte(path))
	dir := filepath.Join(s.home, "pages", hex.EncodeToString(sum[:8]))
	l, err := eventlog.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "page.txt"), []byte(path+"\n"), 0o600); err != nil {
		return nil, err
	}
	s.pages[path] = l
	return l, nil
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)
	var req Request
	if err := dec.Decode(&req); err != nil {
		if !errors.Is(err, io.EOF) {
			reply(enc, Response{Error: err.Error()})
		}
		return
	}
	l, err := s.page(req.Page)
	if err != nil {
		reply(enc, Response{Error: err.Error()})
		return
	}
	switch req.Op {
	case "append":
		e, err := l.Append(&eventlog.Event{Author: req.Author, Kind: req.Kind, Target: req.Target, Text: req.Text})
		if err != nil {
			reply(enc, Response{Error: err.Error()})
			return
		}
		reply(enc, Response{Event: &e})
	case "pending":
		reply(enc, Response{Events: l.Pending(req.Reader)})
	case "ack":
		if err := l.Ack(req.Reader, req.Seq); err != nil {
			reply(enc, Response{Error: err.Error()})
			return
		}
		reply(enc, Response{})
	case "status":
		reply(enc, Response{Readers: s.status(req.Page, l)})
	case "watch":
		s.watch(dec, enc, &req, l)
	default:
		reply(enc, Response{Error: fmt.Sprintf("unknown op %q", req.Op)})
	}
}

func reply(enc *json.Encoder, resp Response) {
	if err := enc.Encode(resp); err != nil {
		log.Printf("reply: %v", err)
	}
}

func (s *Server) watch(dec *json.Decoder, enc *json.Encoder, req *Request, l *eventlog.Log) {
	if req.Reader == "" {
		reply(enc, Response{Error: "watch needs a reader"})
		return
	}
	s.setAttached(req.Page, req.Reader, true)
	defer s.setAttached(req.Page, req.Reader, false)

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
			if err := enc.Encode(Response{Event: &e}); err != nil {
				return
			}
			sent = e.Seq
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
