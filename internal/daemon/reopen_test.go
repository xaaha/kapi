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

func viewingFile(home, path string) string {
	return filepath.Join(home, "pages", page.ID(path), "viewing")
}

func markViewing(t *testing.T, home, path string, age time.Duration) {
	t.Helper()
	f := viewingFile(home, path)
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(f, at, at); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

func startAndCollect(t *testing.T, home string, want int) (o *opener, stop func()) {
	t.Helper()
	o = &opener{}
	_, stop = servingWith(t, home, o.open)
	waitUntil(t, fmt.Sprintf("%d reopens", want), func() bool { return len(o.opened()) >= want })
	time.Sleep(100 * time.Millisecond)
	return o, stop
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

func TestStartReopensPagesThatHadAViewerWhenTheDaemonStopped(t *testing.T) {
	home := shortHome(t)
	logs := captureLog(t)
	var watched []string
	for i := range 3 {
		p := knownPage(t, home, fmt.Sprintf("watched%d.html", i))
		markViewing(t, home, p, time.Duration(i+1)*24*time.Hour)
		watched = append(watched, p)
	}
	gone := knownPage(t, home, "gone.html")
	markViewing(t, home, gone, 0)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	closed := knownPage(t, home, "closed.html")

	o, stop := startAndCollect(t, home, 3)
	defer stop()
	if got, want := o.opened(), pageURLs(t, home, watched); !slices.Equal(got, want) {
		t.Errorf("reopened\n%v\nwant the pages with a viewer when the daemon stopped, newest first\n%v", got, want)
	}
	for _, p := range append(watched, gone, closed) {
		if exists(viewingFile(home, p)) {
			t.Errorf("%s still has a viewing file after the start", p)
		}
	}
	for _, p := range watched {
		if !strings.Contains(logs.String(), "reopened "+p+" in the browser") {
			t.Errorf("daemon log does not record the reopen of %s:\n%s", p, logs)
		}
	}
	if tok := strings.Split(httpURL(t, home), "/")[3]; strings.Contains(logs.String(), tok) {
		t.Errorf("daemon log holds the token:\n%s", logs)
	}
}

func TestARestartLoopWithNoTabReconnectingReopensEachPageOnce(t *testing.T) {
	home := shortHome(t)
	captureLog(t)
	p := knownPage(t, home, "plan.html")
	markViewing(t, home, p, 0)
	o := &opener{}
	for range 3 {
		_, stop := servingWith(t, home, o.open)
		time.Sleep(100 * time.Millisecond)
		stop()
	}
	if got := o.opened(); len(got) != 1 {
		t.Errorf("three starts opened %v, want the page once", got)
	}
}

func TestStartReopensAtMostTheFiveNewestPages(t *testing.T) {
	home := shortHome(t)
	captureLog(t)
	var watched []string
	for i := range 7 {
		p := knownPage(t, home, fmt.Sprintf("watched%d.html", i))
		markViewing(t, home, p, time.Duration(i+1)*time.Minute)
		watched = append(watched, p)
	}
	o, stop := startAndCollect(t, home, 5)
	defer stop()
	if got, want := o.opened(), pageURLs(t, home, watched[:5]); !slices.Equal(got, want) {
		t.Errorf("reopened\n%v\nwant the five newest\n%v", got, want)
	}
}

func TestStartWithoutAnOpenerReopensNothingAndForgetsOldViewers(t *testing.T) {
	home := shortHome(t)
	logs := captureLog(t)
	p := knownPage(t, home, "plan.html")
	_, stop := servingAt(t, home)
	oldURL := call(t, home, &Request{Op: "open", Page: p}).URL
	stop()
	markViewing(t, home, p, 0)
	_, stop = servingWith(t, home, nil)
	defer stop()
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(logs.String(), "reopen") {
		t.Errorf("a daemon with no opener logged a reopen:\n%s", logs)
	}
	if exists(viewingFile(home, p)) {
		t.Errorf("the viewing file outlived a start, so a later start would reopen a dead tab")
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
	closed := knownPage(t, home, "closed.html")

	_, stop := servingAt(t, home)
	var oldURLs []string
	for _, p := range []string{shown, failed, closed} {
		oldURLs = append(oldURLs, call(t, home, &Request{Op: "open", Page: p}).URL)
	}
	stop()
	for _, p := range []string{shown, failed} {
		markViewing(t, home, p, 0)
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

type viewer struct {
	resp *http.Response
}

func connectViewer(t *testing.T, pageURL string) *viewer {
	t.Helper()
	resp, err := http.Get(pageURL + "events") //nolint:gosec // the daemon under test gave this URL
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	return &viewer{resp: resp}
}

func (v *viewer) close() { v.resp.Body.Close() }

func TestTheViewingFileListsAPageWhileAnyViewerIsConnected(t *testing.T) {
	home := shortHome(t)
	path := writePage(t, "<p>x</p>\n")
	_, stop := servingAt(t, home)
	defer stop()
	pageURL := call(t, home, &Request{Op: "open", Page: path}).URL
	viewing := viewingFile(home, path)
	if exists(viewing) {
		t.Fatal("viewing exists before any viewer")
	}
	first := connectViewer(t, pageURL)
	waitUntil(t, "the viewing file once a viewer connects", func() bool { return exists(viewing) })
	fi, err := os.Stat(viewing)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("viewing: %v, %v; want mode 0600", fi, err)
	}
	b, err := os.ReadFile(viewing)
	if err != nil {
		t.Fatal(err)
	}
	if tok := strings.Split(pageURL, "/")[3]; strings.Contains(string(b), tok) {
		t.Errorf("viewing holds the token: %q", b)
	}
	second := connectViewer(t, pageURL)
	first.close()
	time.Sleep(100 * time.Millisecond)
	if !exists(viewing) {
		t.Fatal("viewing went away while a second viewer is still connected")
	}
	second.close()
	waitUntil(t, "the viewing file to go once the last viewer leaves", func() bool { return !exists(viewing) })
}

func TestTheViewingFileSurvivesTheDaemonStopping(t *testing.T) {
	home := shortHome(t)
	path := writePage(t, "<p>x</p>\n")
	_, stop := servingAt(t, home)
	pageURL := call(t, home, &Request{Op: "open", Page: path}).URL
	connectViewer(t, pageURL)
	waitUntil(t, "the viewing file", func() bool { return exists(viewingFile(home, path)) })
	stop()
	time.Sleep(100 * time.Millisecond)
	if !exists(viewingFile(home, path)) {
		t.Error("stopping the daemon removed the viewing file of a page whose tab was open")
	}
}

func TestStartSkipsThePageTheStartingOpenAskedFor(t *testing.T) {
	home := shortHome(t)
	captureLog(t)
	asked := knownPage(t, home, "asked.html")
	other := knownPage(t, home, "other.html")
	_, stop := servingAt(t, home)
	askedURL := call(t, home, &Request{Op: "open", Page: asked}).URL
	stop()
	markViewing(t, home, asked, 0)
	markViewing(t, home, other, time.Minute)

	o := &opener{}
	s := New(home)
	s.Browser = o.open
	s.ReopenExcept = page.ID(asked)
	stop = serveServer(t, home, s)
	defer stop()
	waitUntil(t, "one reopen", func() bool { return len(o.opened()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	if got, want := o.opened(), pageURLs(t, home, []string{other}); !slices.Equal(got, want) {
		t.Errorf("reopened %v, want only %v", got, want)
	}
	if exists(viewingFile(home, asked)) {
		t.Error("the skipped page kept its viewing file")
	}
	if body := reopenBody(t, askedURL); body["reopened"] != nil {
		t.Errorf("the skipped page's old tab is told it was reopened: %v", body)
	}
}

func TestStartIgnoresDirectoriesThatAreNotPageIDs(t *testing.T) {
	home := shortHome(t)
	captureLog(t)
	valid := knownPage(t, home, "real.html")
	markViewing(t, home, valid, 0)
	for _, name := range []string{"ABCDEF0123456789", "0123456789abcdef0", "0123456789abcde", "notapageid", "0123456789abcdeg"} {
		dir := filepath.Join(home, "pages", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "page.txt"), []byte(valid+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "viewing"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	o, stop := startAndCollect(t, home, 1)
	defer stop()
	if got, want := o.opened(), pageURLs(t, home, []string{valid}); !slices.Equal(got, want) {
		t.Errorf("reopened %v, want only the valid page %v", got, want)
	}
}
