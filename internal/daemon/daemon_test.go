package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
)

func TestOnlyOneDaemonServesAHome(t *testing.T) {
	home, err := os.MkdirTemp("", "kd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 8
	results := make(chan error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- New(home).Serve(ctx)
		}()
	}
	close(start)

	deadline := time.After(2 * time.Second)
	refused := 0
	for refused < n-1 {
		select {
		case err := <-results:
			if !errors.Is(err, ErrAlreadyRunning) {
				t.Fatalf("Serve returned %v, want ErrAlreadyRunning for all but one", err)
			}
			refused++
		case <-deadline:
			t.Fatalf("%d daemons are serving the same home, want 1", n-refused)
		}
	}
	reach := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("unix", SocketPath(home))
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(reach) {
			t.Fatalf("the running daemon is unreachable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wg.Wait()
}

func TestDaemonStartsOverAStaleSocket(t *testing.T) {
	home, err := os.MkdirTemp("", "kd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	stale, err := net.Listen("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(home).Serve(ctx) }()
	reach := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("unix", SocketPath(home))
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(reach) {
			t.Fatalf("daemon did not take over the stale socket: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPagesListsEveryKnownPage(t *testing.T) {
	home, err := os.MkdirTemp("", "kd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	s := New(home)
	for _, page := range []string{"/tmp/a.html", "/tmp/a.html", "/tmp/b.html"} {
		l, err := s.page(page)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "hi"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := New(home).listPages()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "/tmp/a.html" || got[1].Path != "/tmp/b.html" {
		t.Fatalf("pages = %+v", got)
	}
	if got[0].Readers[0].Reader != "agent" || got[0].Readers[0].Pending != 2 || got[1].Readers[0].Pending != 1 {
		t.Errorf("readers = %+v / %+v", got[0].Readers, got[1].Readers)
	}
}
