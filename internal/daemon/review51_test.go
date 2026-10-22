package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/page"
)

// kapi open on a page that needs ids, with no browser tab, leaves the moved-aside file next to the page after the daemon stops.
func TestReview51OpenWithoutABrowserLeavesTheHeldFile(t *testing.T) {
	home := shortHome(t)
	path := writePage(t, "<p>needs an id</p>\n")
	_, stop := servingAt(t, home)
	if resp := call(t, home, &Request{Op: "open", Page: path}); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	time.Sleep(1500 * time.Millisecond)
	stop()
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*kapi-*")); len(left) > 0 {
		t.Errorf("after open, 1.5s of polling and a daemon stop, files left next to the page: %v", left)
	}
	if held, _ := filepath.Glob(filepath.Join(home, "pages", page.ID(path), "held", "*")); len(held) > 0 {
		t.Errorf("after a daemon stop, held files left under KAPI_HOME: %v", held)
	}
}

func TestTheDaemonSwapsBackALateWriteWithNoBrowserConnected(t *testing.T) {
	home := shortHome(t)
	path := writePage(t, "<p>needs an id</p>\n")
	_, stop := servingAt(t, home)
	defer stop()
	if resp := call(t, home, &Request{Op: "open", Page: path}); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	held, _ := filepath.Glob(filepath.Join(home, "pages", page.ID(path), "held", "*"))
	if len(held) != 1 {
		t.Fatalf("held files after open = %v, want the one the id write-back moved aside", held)
	}
	late := "<p data-kapi-id=\"aaaaaaaa\">late</p>\n"
	f, err := os.OpenFile(held[0], os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(late); err != nil {
		t.Fatal(err)
	}
	f.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil && string(b) == late {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("page = %q 3s after a late write, want the daemon to swap it back without any request", b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
