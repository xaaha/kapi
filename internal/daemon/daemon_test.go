package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
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
