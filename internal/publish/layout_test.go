package publish_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/client"
	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/publish"
)

func TestPageIDMatchesTheDaemonLayout(t *testing.T) {
	home, err := os.MkdirTemp("", "kp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.New(home).Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.Dial("unix", daemon.SocketPath(home))
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon unreachable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	page := filepath.Join(t.TempDir(), "plan.html")
	if _, err := (client.Client{Home: home}).Do(&daemon.Request{Op: "append", Page: page, Author: "human", Kind: "message", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "pages", publish.PageID(page))); err != nil {
		t.Errorf("PageID(%q) does not name the daemon's page directory: %v", page, err)
	}
}
