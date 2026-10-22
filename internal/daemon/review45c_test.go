package daemon

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
)

func TestTheInboxReadsNoLogOfAPageOutsideTheDirectory(t *testing.T) {
	home, s, stop := serving(t)
	inside := filepath.Join(t.TempDir(), "in.html")
	outside := filepath.Join(t.TempDir(), "out.html")
	for _, p := range []string{inside, outside} {
		call(t, home, &Request{Op: "append", Page: p, Author: "human", Kind: "message", Text: "hi"})
	}
	_ = s
	stop()
	outLog := filepath.Join(home, "pages", page.ID(outside), "events.ndjson")
	if err := os.Remove(outLog); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(outLog, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ = servingAt(t, home)
	replies := make(chan Response, 1)
	go func() {
		conn, err := net.Dial("unix", SocketPath(home))
		if err != nil {
			return
		}
		defer conn.Close()
		if json.NewEncoder(conn).Encode(Request{Op: "inbox", Page: filepath.Dir(inside), Reader: "agent"}) != nil {
			return
		}
		var r Response
		if json.NewDecoder(conn).Decode(&r) == nil {
			replies <- r
		}
	}()
	select {
	case got := <-replies:
		if len(got.Pages) != 1 || got.Pages[0].Path != inside {
			t.Fatalf("inbox = %+v", got.Pages)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the inbox read the event log of a page outside the hook's directory; every tool call would read every log")
	}
	s.mu.Lock()
	_, opened := s.pages[outside]
	s.mu.Unlock()
	if opened {
		t.Error("the inbox opened the event log of a page outside the hook's directory; every tool call would read every log")
	}
}

func BenchmarkInboxWithManyLargePagesElsewhere(b *testing.B) {
	home := b.TempDir()
	var log bytes.Buffer
	for i := range 20000 {
		line, err := json.Marshal(eventlog.Event{Seq: uint64(i + 1), Author: "human", Kind: "message", Text: "hello there"})
		if err != nil {
			b.Fatal(err)
		}
		log.Write(append(line, '\n'))
	}
	for range 40 {
		path := filepath.Join(b.TempDir(), "p.html")
		dir := filepath.Join(home, "pages", page.ID(path))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "page.txt"), []byte(path+"\n"), 0o600); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "events.ndjson"), log.Bytes(), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	s := New(home)
	dir := b.TempDir()
	b.ResetTimer()
	for range b.N {
		if _, err := s.inbox(dir, "agent"); err != nil {
			b.Fatal(err)
		}
	}
}
