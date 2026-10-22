package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/page"
)

type opener struct {
	mu   sync.Mutex
	urls []string
	fail map[string]bool
}

func (o *opener) open(url string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.urls = append(o.urls, url)
	for id := range o.fail {
		if strings.Contains(url, "/p/"+id+"/") {
			return errors.New("no browser")
		}
	}
	return nil
}

func (o *opener) opened() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.urls)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	b := &lockedBuffer{}
	prev := log.Writer()
	log.SetOutput(b)
	t.Cleanup(func() { log.SetOutput(prev) })
	return b
}

func knownPage(t *testing.T, home, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("<p>x</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "pages", page.ID(path))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.txt"), []byte(path+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func viewedAgo(t *testing.T, home, path string, ago time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-ago).UTC().Format(time.RFC3339Nano) + "\n"
	if err := os.WriteFile(filepath.Join(home, "pages", page.ID(path), "viewed"), []byte(stamp), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startAndCollect(t *testing.T, home string, want int) []string {
	t.Helper()
	o := &opener{}
	_, stop := servingWith(t, home, o.open)
	t.Cleanup(stop)
	waitUntil(t, fmt.Sprintf("%d reopens", want), func() bool { return len(o.opened()) >= want })
	time.Sleep(100 * time.Millisecond)
	return o.opened()
}

func pageURLs(t *testing.T, home string, paths []string) []string {
	t.Helper()
	base := httpURL(t, home)
	var out []string
	for _, p := range paths {
		out = append(out, base+"p/"+page.ID(p)+"/")
	}
	return out
}

func TestStartReopensRecentlyViewedPagesWhoseFileStillExists(t *testing.T) {
	home := shortHome(t)
	logs := captureLog(t)
	var recent []string
	for i := range 3 {
		p := knownPage(t, home, fmt.Sprintf("recent%d.html", i))
		viewedAgo(t, home, p, time.Duration(i+1)*time.Hour-time.Minute)
		recent = append(recent, p)
	}
	stale := knownPage(t, home, "stale.html")
	viewedAgo(t, home, stale, 3*time.Hour+time.Minute)
	gone := knownPage(t, home, "gone.html")
	viewedAgo(t, home, gone, 30*time.Second)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	knownPage(t, home, "never.html")

	got := startAndCollect(t, home, 3)
	if want := pageURLs(t, home, recent); !slices.Equal(got, want) {
		t.Errorf("reopened\n%v\nwant the pages viewed in the last 3 hours whose file exists, newest first\n%v", got, want)
	}
	for _, p := range recent {
		if !strings.Contains(logs.String(), "reopened "+p+" in the browser") {
			t.Errorf("daemon log does not record the reopen of %s:\n%s", p, logs)
		}
	}
	if tok := strings.Split(httpURL(t, home), "/")[3]; strings.Contains(logs.String(), tok) {
		t.Errorf("daemon log holds the token:\n%s", logs)
	}
}

func TestStartReopensAtMostTheFiveNewestPages(t *testing.T) {
	home := shortHome(t)
	captureLog(t)
	var recent []string
	for i := range 7 {
		p := knownPage(t, home, fmt.Sprintf("recent%d.html", i))
		viewedAgo(t, home, p, time.Duration(i+1)*time.Minute)
		recent = append(recent, p)
	}
	got := startAndCollect(t, home, 5)
	if want := pageURLs(t, home, recent[:5]); !slices.Equal(got, want) {
		t.Errorf("reopened\n%v\nwant the five newest\n%v", got, want)
	}
}

func TestStartReopensNothingWithoutAnOpener(t *testing.T) {
	home := shortHome(t)
	logs := captureLog(t)
	p := knownPage(t, home, "plan.html")
	_, stop := servingAt(t, home)
	oldURL := call(t, home, &Request{Op: "open", Page: p}).URL
	stop()
	viewedAgo(t, home, p, time.Minute)
	_, stop = servingWith(t, home, nil)
	defer stop()
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(logs.String(), "reopen") {
		t.Errorf("a daemon with no opener logged a reopen:\n%s", logs)
	}
	if body := reopenBody(t, oldURL); body["reopened"] != nil || body["reopen"] != "kapi open "+p {
		t.Errorf("403 body %v, want the reopen command and no reopened", body)
	}
}

func reopenBody(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // built from the daemon under test
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET %s: %s %v", url, resp.Status, err)
	}
	return body
}

func TestTheRestartNoticeSaysReopenedOnlyForPagesReopenedAtThisStart(t *testing.T) {
	home := shortHome(t)
	captureLog(t)
	shown := knownPage(t, home, "shown.html")
	failed := knownPage(t, home, "failed.html")
	old := knownPage(t, home, "old.html")
	for _, p := range []string{shown, failed} {
		viewedAgo(t, home, p, time.Minute)
	}
	viewedAgo(t, home, old, 4*time.Hour)

	_, stop := servingAt(t, home)
	var oldURLs []string
	for _, p := range []string{shown, failed, old} {
		oldURLs = append(oldURLs, call(t, home, &Request{Op: "open", Page: p}).URL)
	}
	stop()
	for _, p := range []string{shown, failed} {
		viewedAgo(t, home, p, time.Minute)
	}

	o := &opener{fail: map[string]bool{page.ID(failed): true}}
	_, stop = servingWith(t, home, o.open)
	defer stop()
	waitUntil(t, "two reopen attempts", func() bool { return len(o.opened()) == 2 })
	time.Sleep(50 * time.Millisecond)

	for i, want := range []bool{true, false, false} {
		for _, suffix := range []string{"", "events"} {
			body := reopenBody(t, oldURLs[i]+suffix)
			if got := body["reopened"] == true; got != want {
				t.Errorf("GET %s: %v, want reopened %v", oldURLs[i]+suffix, body, want)
			}
			if !want && len(body) != 2 {
				t.Errorf("GET %s: %v, want only error and reopen", oldURLs[i]+suffix, body)
			}
		}
	}
	u := httpURL(t, home)
	forged := "http://" + strings.Split(u, "/")[2] + "/" + strings.Repeat("cd", 16) + "/p/" + page.ID(shown) + "/"
	if body := reopenBody(t, forged); body["reopened"] != nil {
		t.Errorf("a token never issued learns of the reopen: %v", body)
	}
}

func TestAnOldTabsPostsGetTheRestartNotice(t *testing.T) {
	home := shortHome(t)
	path := writePage(t, `<p data-kapi-id="aaaaaaaa">x</p>`+"\n")
	_, stop := servingAt(t, home)
	pageURL := call(t, home, &Request{Op: "open", Page: path}).URL
	stop()
	_, stop = servingAt(t, home)
	defer stop()
	host := strings.Split(pageURL, "/")[2]
	for _, tc := range []struct{ path, body string }{
		{"messages", `{"kind":"message","text":"hi"}`},
		{"ops", `{"rev":1,"op":{"kind":"done","id":"aaaaaaaa"}}`},
		{"answers", `{"form":"f","values":{}}`},
	} {
		r, err := http.NewRequest("POST", pageURL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://"+host)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || err != nil || body["error"] != "kapi restarted" || body["reopen"] != "kapi open "+path {
			t.Errorf("POST %s from the old tab: %s %v %v, want the restart notice", tc.path, resp.Status, body, err)
		}
	}
}

func TestTheEventsStreamRecordsWhenAViewerWasLastConnected(t *testing.T) {
	home := shortHome(t)
	path := writePage(t, "<p>x</p>\n")
	_, stop := servingAt(t, home)
	defer stop()
	pageURL := call(t, home, &Request{Op: "open", Page: path}).URL
	viewed := filepath.Join(home, "pages", page.ID(path), "viewed")
	if _, err := os.Stat(viewed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("viewed exists before any viewer: %v", err)
	}
	before := time.Now().Add(-time.Second)
	resp, err := http.Get(pageURL + "events") //nolint:gosec // the daemon under test gave this URL
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the viewed file while the viewer is connected", func() bool { _, err := os.Stat(viewed); return err == nil })
	fi, err := os.Stat(viewed)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("viewed: %v, %v; want mode 0600", fi, err)
	}
	b, err := os.ReadFile(viewed)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil || at.Before(before) || at.After(time.Now()) {
		t.Errorf("viewed = %q (%v), want the time of the connection", b, err)
	}
	if tok := strings.Split(pageURL, "/")[3]; strings.Contains(string(b), tok) {
		t.Errorf("viewed holds the token: %q", b)
	}
}
