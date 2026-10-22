package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheDaemonGetsABrowserOnlyWhenStartedOnDemandWithADisplay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reopen bool
		env    map[string]string
		goos   string
		want   bool
	}{
		{"on demand on macOS", true, nil, "darwin", true},
		{"on demand on Linux with X", true, map[string]string{"DISPLAY": ":0"}, "linux", true},
		{"on demand on Linux with Wayland", true, map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "linux", true},
		{"foreground", false, map[string]string{"DISPLAY": ":0"}, "linux", false},
		{"foreground on macOS", false, nil, "darwin", false},
		{"KAPI_NO_BROWSER", true, map[string]string{"KAPI_NO_BROWSER": "1"}, "darwin", false},
		{"KAPI_NO_BROWSER with a display", true, map[string]string{"KAPI_NO_BROWSER": "1", "DISPLAY": ":0"}, "linux", false},
		{"Linux without a display", true, nil, "linux", false},
		{"SSH on macOS", true, map[string]string{"SSH_CONNECTION": "10.0.0.1 22 10.0.0.2 22"}, "darwin", false},
		{"SSH TTY on Linux with a display", true, map[string]string{"SSH_TTY": "/dev/pts/0", "DISPLAY": ":0"}, "linux", false},
		{"CI on macOS", true, map[string]string{"CI": "true"}, "darwin", false},
		{"CI on Linux with a display", true, map[string]string{"CI": "1", "DISPLAY": ":0"}, "linux", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := stubBrowser(t, nil)
			b := daemonBrowser(tc.reopen, func(k string) string { return tc.env[k] }, tc.goos)
			if (b != nil) != tc.want {
				t.Fatalf("browser set = %v, want %v", b != nil, tc.want)
			}
			if b == nil {
				return
			}
			if err := b("http://127.0.0.1:1/t/p/x/"); err != nil {
				t.Fatal(err)
			}
			if len(*opened) != 1 || (*opened)[0] != "http://127.0.0.1:1/t/p/x/" {
				t.Errorf("the daemon's browser did not go through the kapi open opener: %v", *opened)
			}
		})
	}
}

func (e *e2e) view(url string) (closeTab func()) {
	e.t.Helper()
	resp, err := http.Get(url + "events") //nolint:gosec // the URL kapi open printed
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		e.t.Fatal(err)
	}
	return func() { resp.Body.Close() }
}

func TestADaemonStartedOnDemandReopensAPageWhoseTabWasOpen(t *testing.T) {
	e := newE2E(t)
	e.env = append(e.env, "DISPLAY=:99")
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldURL := strings.TrimSpace(e.kapi("open", "--print", e.page))
	e.view(oldURL)
	e.killDaemon()
	e.kapi("send", e.page, "wakes the daemon")

	waitFor(t, "the reopen", func() bool { b, _ := os.ReadFile(e.opened); return len(b) > 0 })
	b, err := os.ReadFile(e.opened)
	if err != nil {
		t.Fatal(err)
	}
	newURL := strings.TrimSpace(e.kapi("open", "--print", e.page))
	if got := strings.TrimSpace(string(b)); got != newURL || got == oldURL {
		t.Errorf("reopened %q, want the new URL %q", got, newURL)
	}
	resp, err := http.Get(oldURL + "events") //nolint:gosec // the URL kapi open printed
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	err = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if err != nil || body["reopened"] != true {
		t.Errorf("old tab's notice = %v %v, want reopened", body, err)
	}
	logf, err := os.ReadFile(filepath.Join(e.home, "daemon.log"))
	if err != nil || !strings.Contains(string(logf), "reopened "+e.page+" in the browser") {
		t.Errorf("daemon.log = %q %v, want the reopen recorded", logf, err)
	}
}

func TestADaemonStartedOnDemandWithKapiNoBrowserReopensNothing(t *testing.T) {
	e := newE2E(t)
	e.env = append(e.env, "DISPLAY=:99", "KAPI_NO_BROWSER=1")
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.view(strings.TrimSpace(e.kapi("open", "--print", e.page)))
	e.killDaemon()
	e.kapi("send", e.page, "wakes the daemon")
	time.Sleep(500 * time.Millisecond)
	if b, err := os.ReadFile(e.opened); err == nil {
		t.Errorf("KAPI_NO_BROWSER still reopened %q", b)
	}
}

func TestADaemonStartedOnDemandLeavesAClosedTabClosed(t *testing.T) {
	e := newE2E(t)
	e.env = append(e.env, "DISPLAY=:99")
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	closeTab := e.view(strings.TrimSpace(e.kapi("open", "--print", e.page)))
	closeTab()
	time.Sleep(200 * time.Millisecond)
	e.killDaemon()
	e.kapi("send", e.page, "wakes the daemon")
	time.Sleep(500 * time.Millisecond)
	if b, err := os.ReadFile(e.opened); err == nil {
		t.Errorf("a tab closed before the restart was reopened: %q", b)
	}
}

func TestOpenAfterARestartOpensThePageOnce(t *testing.T) {
	e := newE2E(t)
	e.env = append(e.env, "DISPLAY=:99")
	if err := os.WriteFile(e.page, []byte("<h1>Plan</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.view(strings.TrimSpace(e.kapi("open", "--print", e.page)))
	e.killDaemon()
	e.kapi("open", e.page)
	time.Sleep(500 * time.Millisecond)
	b, err := os.ReadFile(e.opened)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) != 1 {
		t.Errorf("kapi open after a restart opened %d tabs: %q", len(lines), lines)
	}
}
