package daemon

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/web/app"
)

type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

type archiveSite struct {
	t     *testing.T
	home  string
	dir   string
	s     *Server
	clock *fakeClock
	stop  func()
}

func archiving(t *testing.T, settingsJSON string) *archiveSite {
	t.Helper()
	a := &archiveSite{t: t, home: shortHome(t), dir: t.TempDir(), clock: &fakeClock{at: time.Now().UTC().Truncate(time.Second)}}
	if settingsJSON != "" {
		if err := os.WriteFile(filepath.Join(a.home, "settings.json"), []byte(settingsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a.start()
	return a
}

func (a *archiveSite) start() {
	a.t.Helper()
	a.s = New(a.home)
	a.s.now = a.clock.now
	a.stop = serveServer(a.t, a.home, a.s)
}

func (a *archiveSite) restart() {
	a.t.Helper()
	a.stop()
	a.start()
}

func (a *archiveSite) page(name string, untouchedFor time.Duration) (path, url string) {
	a.t.Helper()
	path = filepath.Join(a.dir, name)
	if err := os.WriteFile(path, []byte("<h1>"+name+"</h1>\n"), 0o600); err != nil {
		a.t.Fatal(err)
	}
	at := a.clock.now().Add(-untouchedFor)
	if err := os.Chtimes(path, at, at); err != nil {
		a.t.Fatal(err)
	}
	resp := call(a.t, a.home, &Request{Op: "open", Page: path})
	if resp.Error != "" {
		a.t.Fatal(resp.Error)
	}
	return path, resp.URL
}

func (a *archiveSite) lifecycle(path string) page.Lifecycle {
	a.t.Helper()
	lc, err := page.ReadLifecycle(filepath.Join(a.home, "pages", page.ID(path)))
	if err != nil {
		a.t.Fatal(err)
	}
	return lc
}

func (a *archiveSite) archived(path string) bool {
	a.t.Helper()
	return !a.lifecycle(path).Archived.IsZero()
}

func (a *archiveSite) indexed(path string) app.Page {
	a.t.Helper()
	pages, err := a.s.Index()
	if err != nil {
		a.t.Fatal(err)
	}
	for i := range pages {
		if pages[i].Path == path {
			return pages[i]
		}
	}
	a.t.Fatalf("%s is not in the index", path)
	return app.Page{}
}

func (a *archiveSite) sweepAfter(d time.Duration) {
	a.clock.add(d)
	a.s.sweepArchive()
}

func TestTheSweepArchivesAPageOnceItIsUntouchedForArchiveAfter(t *testing.T) {
	a := archiving(t, "")
	fresh, _ := a.page("fresh.html", 23*time.Hour)
	stale, _ := a.page("stale.html", 25*time.Hour)
	a.sweepAfter(archiveGrace)
	if a.archived(fresh) {
		t.Error("a page touched 23 hours ago was archived")
	}
	if got := a.lifecycle(stale).Archived; !got.Equal(a.clock.now()) {
		t.Fatalf("a page touched 25 hours ago has archivedAt %v, want %v", got, a.clock.now())
	}
	p := a.indexed(stale)
	if !p.ArchivedAt.Equal(a.clock.now()) || !p.DeletesAt.Equal(a.clock.now().Add(720*time.Hour)) || !p.ArchivesAt.IsZero() {
		t.Errorf("archived page in the index: archivedAt %v deletesAt %v archivesAt %v", p.ArchivedAt, p.DeletesAt, p.ArchivesAt)
	}
	if p := a.indexed(fresh); !p.ArchivesAt.Equal(p.Touched.Add(24*time.Hour)) || !p.ArchivedAt.IsZero() {
		t.Errorf("temporary page in the index: archivesAt %v archivedAt %v, want archivesAt a day after %v", p.ArchivesAt, p.ArchivedAt, p.Touched)
	}
	a.sweepAfter(time.Hour)
	if !a.archived(fresh) {
		t.Error("a page untouched for over a day stayed in Recent")
	}
}

func TestArchiveAfterZeroTurnsArchivingOff(t *testing.T) {
	a := archiving(t, `{"retention":{"archiveAfter":"0"}}`)
	stale, _ := a.page("stale.html", 90*24*time.Hour)
	a.sweepAfter(archiveGrace)
	if a.archived(stale) {
		t.Error("archiving is off, but the sweep archived a page")
	}
	if p := a.indexed(stale); !p.ArchivesAt.IsZero() {
		t.Errorf("archiving is off, but the index says the page archives at %v", p.ArchivesAt)
	}
}

func TestASavedPageIsNeverArchived(t *testing.T) {
	a := archiving(t, "")
	saved, _ := a.page("saved.html", 48*time.Hour)
	a.stop()
	state := filepath.Join(a.home, "pages", page.ID(saved), "state.json")
	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, append(bytes.TrimSuffix(b, []byte("}")), []byte(`,"saved":true}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	a.start()
	a.sweepAfter(archiveGrace)
	if a.archived(saved) {
		t.Error("the sweep archived a saved page")
	}
	if p := a.indexed(saved); !p.ArchivesAt.IsZero() {
		t.Errorf("the index says a saved page archives at %v", p.ArchivesAt)
	}
}

func TestAnOpenTabKeepsAPageAndItsClockStartsWhenTheTabCloses(t *testing.T) {
	a := archiving(t, "")
	path, pageURL := a.page("open.html", 48*time.Hour)
	tab := connectViewer(t, pageURL)
	waitUntil(t, "the tab to count", func() bool { return a.s.inUse(page.ID(path), path) })
	a.sweepAfter(archiveGrace)
	if a.archived(path) {
		t.Fatal("the sweep archived a page open in a tab")
	}
	if p := a.indexed(path); !p.ArchivesAt.IsZero() {
		t.Errorf("the index says an open page archives at %v", p.ArchivesAt)
	}
	a.clock.add(72 * time.Hour)
	closed := a.clock.now()
	tab.close()
	waitUntil(t, "the tab to close", func() bool { return !a.s.inUse(page.ID(path), path) })
	if p := a.indexed(path); !p.ArchivesAt.Equal(closed.Add(24 * time.Hour)) {
		t.Errorf("after the tab closed, archivesAt %v, want a day after it closed (%v)", p.ArchivesAt, closed)
	}
	a.sweepAfter(23 * time.Hour)
	if a.archived(path) {
		t.Fatal("the page was archived less than a day after its tab closed")
	}
	a.sweepAfter(time.Hour)
	if !a.archived(path) {
		t.Error("the page stayed in Recent a day after its tab closed")
	}
}

func TestAnAttachedAgentKeepsAPageAndItsClockStartsWhenItDetaches(t *testing.T) {
	a := archiving(t, "")
	path, _ := a.page("watched.html", 48*time.Hour)
	conn, err := net.Dial("unix", SocketPath(a.home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "watch", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the agent to attach", func() bool { return a.s.inUse(page.ID(path), path) })
	a.sweepAfter(archiveGrace + 48*time.Hour)
	if a.archived(path) {
		t.Fatal("the sweep archived a page an agent is watching")
	}
	detached := a.clock.now()
	conn.Close()
	waitUntil(t, "the agent to detach", func() bool { return !a.s.inUse(page.ID(path), path) })
	a.sweepAfter(23 * time.Hour)
	if a.archived(path) {
		t.Fatal("the page was archived less than a day after the agent left")
	}
	a.sweepAfter(time.Hour)
	if got := a.lifecycle(path).Archived; !got.Equal(detached.Add(24 * time.Hour)) {
		t.Errorf("archivedAt %v, want a day after the agent left (%v)", got, detached)
	}
}

func TestKeepWhileOpenOffArchivesOpenPagesWithoutAGracePeriod(t *testing.T) {
	a := archiving(t, `{"retention":{"keepWhileOpen":false}}`)
	path, pageURL := a.page("open.html", 48*time.Hour)
	connectViewer(t, pageURL)
	waitUntil(t, "the tab to count", func() bool { return a.s.inUse(page.ID(path), path) })
	a.sweepAfter(0)
	if !a.archived(path) {
		t.Error("with keepWhileOpen off, an open page untouched for two days stayed in Recent")
	}
}

func TestNothingIsArchivedUntilTabsHadTimeToReconnectAfterAStart(t *testing.T) {
	a := archiving(t, "")
	stale, _ := a.page("stale.html", 48*time.Hour)
	wasOpen, _ := a.page("was-open.html", 48*time.Hour)
	a.stop()
	if err := os.WriteFile(filepath.Join(a.home, "pages", page.ID(wasOpen), viewingName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a.start()
	started := a.clock.now()
	a.sweepAfter(0)
	a.sweepAfter(archiveGrace - time.Second)
	if a.archived(stale) || a.archived(wasOpen) {
		t.Fatal("the sweep archived a page during the grace period after a start")
	}
	a.sweepAfter(time.Second)
	if !a.archived(stale) {
		t.Error("the grace period ended, but a page untouched for two days stayed in Recent")
	}
	if a.archived(wasOpen) {
		t.Error("a page whose tab was open when the daemon stopped was archived before a day had passed since the start")
	}
	if p := a.indexed(wasOpen); !p.ArchivesAt.Equal(started.Add(24 * time.Hour)) {
		t.Errorf("a page open at the last stop archives at %v, want a day after the start (%v)", p.ArchivesAt, started)
	}
}

func TestATouchBringsAnArchivedPageBackToRecent(t *testing.T) {
	a := archiving(t, "")
	noted, pageURL := a.page("noted.html", 48*time.Hour)
	edited, _ := a.page("edited.html", 48*time.Hour)
	waited, _ := a.page("waited.html", 48*time.Hour)
	a.sweepAfter(archiveGrace)
	for _, path := range []string{noted, edited, waited} {
		if !a.archived(path) {
			t.Fatalf("%s was not archived", filepath.Base(path))
		}
	}

	u, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("POST", pageURL+"messages", strings.NewReader(`{"kind":"message","text":"still need this"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", u.Scheme+"://"+u.Host)
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST messages: %s", resp.Status)
	}
	if a.archived(noted) {
		t.Error("a human message left the page archived")
	}

	a.stop()
	if err := os.WriteFile(edited, []byte("<h1>edited by an agent</h1>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.start()
	a.sweepAfter(0)
	if a.archived(edited) {
		t.Error("an agent's edit to the file left the page archived")
	}
	if p := a.indexed(edited); !p.ArchivedAt.IsZero() || p.ArchivesAt.IsZero() {
		t.Errorf("after the edit the index has archivedAt %v archivesAt %v, want it temporary again", p.ArchivedAt, p.ArchivesAt)
	}

	call(t, a.home, &Request{Op: "append", Page: waited, Author: "agent", Kind: "message", Text: "done"})
	a.sweepAfter(archiveGrace)
	if !a.archived(waited) {
		t.Error("an agent message is not a touch, but it brought the page back")
	}
}

func TestArchivedAtSurvivesARestart(t *testing.T) {
	a := archiving(t, "")
	stale, _ := a.page("stale.html", 48*time.Hour)
	a.sweepAfter(archiveGrace)
	at := a.lifecycle(stale).Archived
	if at.IsZero() {
		t.Fatal("the page was not archived")
	}
	a.restart()
	a.sweepAfter(archiveGrace)
	if got := a.indexed(stale).ArchivedAt; !got.Equal(at) {
		t.Errorf("after a restart and another sweep, archivedAt %v, want %v", got, at)
	}
	if _, _, err := a.s.Page(page.ID(stale)); err != nil {
		t.Fatal(err)
	}
	if got := a.lifecycle(stale).Archived; !got.Equal(at) {
		t.Errorf("opening the page's store moved archivedAt to %v, want %v", got, at)
	}
}

func TestARestartKeepsTheTimeTheLastTabClosed(t *testing.T) {
	a := archiving(t, "")
	path, pageURL := a.page("viewed.html", 48*time.Hour)
	tab := connectViewer(t, pageURL)
	waitUntil(t, "the tab to count", func() bool { return a.s.inUse(page.ID(path), path) })
	a.clock.add(72 * time.Hour)
	closed := a.clock.now()
	tab.close()
	waitUntil(t, "the tab to close", func() bool { return !a.s.inUse(page.ID(path), path) })
	a.clock.add(time.Hour)
	a.restart()
	a.sweepAfter(archiveGrace)
	if a.archived(path) {
		t.Fatal("after a restart, a page whose tab closed an hour ago was archived")
	}
	if p := a.indexed(path); !p.ArchivesAt.Equal(closed.Add(24 * time.Hour)) {
		t.Errorf("after a restart, archivesAt %v, want a day after the tab closed (%v)", p.ArchivesAt, closed)
	}
}

func TestAnAgentAttachedAtShutdownCountsAsLeavingAtTheNextStart(t *testing.T) {
	a := archiving(t, "")
	path, _ := a.page("watched.html", 48*time.Hour)
	conn, err := net.Dial("unix", SocketPath(a.home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "watch", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the agent to attach", func() bool { return a.s.inUse(page.ID(path), path) })
	a.clock.add(72 * time.Hour)
	old := a.s
	a.stop()
	conn.Close()
	waitUntil(t, "the agent to detach from the stopped daemon", func() bool { return !old.inUse(page.ID(path), path) })
	a.clock.add(time.Hour)
	a.start()
	started := a.clock.now()
	a.sweepAfter(archiveGrace)
	if a.archived(path) {
		t.Fatal("a page an agent was watching when the daemon stopped was archived after the grace period")
	}
	if p := a.indexed(path); !p.ArchivesAt.Equal(started.Add(24 * time.Hour)) {
		t.Errorf("archivesAt %v, want a day after the start (%v)", p.ArchivesAt, started)
	}
}

func TestUnreadableSettingsStopTheSweepInsteadOfUsingDefaults(t *testing.T) {
	a := archiving(t, `{"retention":{"archiveAfter":"0"}},`)
	stale, _ := a.page("stale.html", 48*time.Hour)
	a.sweepAfter(archiveGrace)
	if a.archived(stale) {
		t.Error("settings.json cannot be read, but the sweep archived a page with the default timer")
	}
	if p := a.indexed(stale); !p.ArchivesAt.IsZero() {
		t.Errorf("settings.json cannot be read, but the index says the page archives at %v", p.ArchivesAt)
	}
}

func TestATabThatConnectsBeforeTheWriteStopsTheArchive(t *testing.T) {
	a := archiving(t, "")
	path, pageURL := a.page("late.html", 48*time.Hour)
	a.clock.add(archiveGrace)
	touched := a.lifecycle(path).Touched
	connectViewer(t, pageURL)
	waitUntil(t, "the tab to count", func() bool { return a.s.inUse(page.ID(path), path) })
	d := pageDir{path: path, dir: filepath.Join(a.home, "pages", page.ID(path))}
	archived, err := a.s.markArchived(d, a.clock.now(), touched, true)
	if err != nil {
		t.Fatal(err)
	}
	if archived || a.archived(path) {
		t.Error("a page was archived although a tab connected after the sweep checked it")
	}
}
