package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
)

const deleteAfter = 720 * time.Hour

func expiring(t *testing.T, retention string) (*archiveSite, string) {
	t.Helper()
	library := t.TempDir()
	settings := map[string]any{"library": library}
	if retention != "" {
		var r map[string]any
		if err := json.Unmarshal([]byte(retention), &r); err != nil {
			t.Fatal(err)
		}
		settings["retention"] = r
	}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return archiving(t, string(b)), library
}

func (a *archiveSite) draft(library, name string, untouchedFor time.Duration) string {
	a.t.Helper()
	path := filepath.Join(library, "drafts", name)
	mustWrite(a.t, path, "<h1>"+name+"</h1>\n")
	mustWrite(a.t, filepath.Join(page.FilesDir(path), "a.png"), "png")
	at := a.clock.now().Add(-untouchedFor)
	if err := os.Chtimes(path, at, at); err != nil {
		a.t.Fatal(err)
	}
	if resp := call(a.t, a.home, &Request{Op: "open", Page: path}); resp.Error != "" {
		a.t.Fatal(resp.Error)
	}
	return path
}

func (a *archiveSite) known(path string) bool {
	a.t.Helper()
	_, err := os.Stat(filepath.Join(a.home, "pages", page.ID(path), "page.txt"))
	return err == nil
}

func (a *archiveSite) inTrash(name string) bool {
	_, err := os.Lstat(filepath.Join(a.home, "trash", "files", name))
	return err == nil
}

func (a *archiveSite) archiveNow(paths ...string) time.Time {
	a.t.Helper()
	a.sweepAfter(archiveGrace)
	for _, p := range paths {
		if !a.archived(p) {
			a.t.Fatalf("%s was not archived", filepath.Base(p))
		}
	}
	return a.clock.now()
}

func TestTheSweepTrashesADraftOnceDeleteAfterPassedSinceItWasArchived(t *testing.T) {
	a, library := expiring(t, "")
	path := a.draft(library, "old.html", 48*time.Hour)
	id := page.ID(path)
	a.archiveNow(path)
	a.sweepAfter(deleteAfter - time.Second)
	if !a.known(path) || a.inTrash("old.html") {
		t.Fatal("the sweep removed a page a second before archivedAt plus deleteAfter")
	}
	a.sweepAfter(time.Second)
	missing(t, path)
	missing(t, page.FilesDir(path))
	if !a.inTrash("old.html") || !a.inTrash("old.files") {
		t.Fatal("the draft and its images folder are not in the Trash")
	}
	if a.known(path) || indexed(t, a.s, id) {
		t.Fatal("kapi still lists the deleted draft")
	}
	l, err := eventlog.Open(filepath.Join(a.home, "deleted", id, stateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	events, _ := l.After(0)
	last := events[len(events)-1]
	if last.Author != "kapi" || last.Kind != eventlog.KindEnd || last.Text != eventlog.EndDeleted {
		t.Fatalf("the set-aside log ends with %+v, want kapi's deleted end", last)
	}
	if resp := call(t, a.home, &Request{Op: "wait", Page: path, Reader: "agent"}); !strings.Contains(resp.Error, "was deleted and is in the Trash") {
		t.Fatalf("a wait on the deleted draft: %+v", resp)
	}
}

func TestDeleteAfterZeroKeepsArchivedPagesForever(t *testing.T) {
	a, library := expiring(t, `{"deleteAfter":"0"}`)
	path := a.draft(library, "kept.html", 48*time.Hour)
	a.archiveNow(path)
	a.sweepAfter(100 * deleteAfter)
	if !a.known(path) || !a.archived(path) || a.inTrash("kept.html") {
		t.Fatal("deletion is off, but the sweep removed an archived page")
	}
}

func TestADeleteAfterShorterThanArchiveAfterCountsFromArchivedAt(t *testing.T) {
	a, library := expiring(t, `{"archiveAfter":"48h","deleteAfter":"1h"}`)
	path := a.draft(library, "short.html", 47*time.Hour)
	a.sweepAfter(archiveEvery)
	if a.archived(path) || !a.known(path) {
		t.Fatal("the page was archived or removed before archiveAfter")
	}
	a.sweepAfter(time.Hour)
	if !a.archived(path) || !a.known(path) {
		t.Fatal("the page should be archived and still kept: deleteAfter counts from archivedAt, not from the last touch")
	}
	a.sweepAfter(time.Hour - time.Second)
	if !a.known(path) {
		t.Fatal("the page was removed before an hour in Archived")
	}
	a.sweepAfter(time.Second)
	if a.known(path) || !a.inTrash("short.html") {
		t.Fatal("the page stayed after an hour in Archived")
	}
}

func TestNothingIsDeletedUntilAFullSweepIntervalAfterAStart(t *testing.T) {
	a, library := expiring(t, "")
	path := a.draft(library, "returning.html", 48*time.Hour)
	a.archiveNow(path)
	a.stop()
	a.clock.add(2 * deleteAfter)
	a.start()
	a.sweepAfter(0)
	a.sweepAfter(archiveEvery - time.Second)
	if !a.known(path) || a.inTrash("returning.html") {
		t.Fatal("the sweep deleted a page before a full sweep interval after the start")
	}
	if p := a.indexed(path); p.ArchivedAt.IsZero() {
		t.Fatal("the returning page is not in Archived")
	}
	a.sweepAfter(time.Second)
	if a.known(path) || !a.inTrash("returning.html") {
		t.Fatal("a full sweep interval after the start, the expired page is still there")
	}
}

func TestAnExpiredPageOutsideTheLibraryIsOnlyForgotten(t *testing.T) {
	a, _ := expiring(t, "")
	path, _ := a.page("repo.html", 48*time.Hour)
	mustWrite(t, filepath.Join(page.FilesDir(path), "a.png"), "png")
	src := mustRead(t, path)
	id := page.ID(path)
	a.archiveNow(path)
	a.sweepAfter(deleteAfter)
	if mustRead(t, path) != src || mustRead(t, filepath.Join(page.FilesDir(path), "a.png")) != "png" {
		t.Fatal("the sweep changed a file outside the library")
	}
	if a.inTrash("repo.html") || a.inTrash("repo.files") {
		t.Fatal("the sweep moved a file outside the library to the Trash")
	}
	missing(t, filepath.Join(a.home, "pages", id))
	if indexed(t, a.s, id) {
		t.Fatal("home still lists the forgotten page")
	}
	l, err := eventlog.Open(filepath.Join(a.home, "deleted", id, stateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	events, _ := l.After(0)
	if last := events[len(events)-1]; last.Author != "kapi" || last.Kind != eventlog.KindEnd || last.Text != eventlog.EndForgotten {
		t.Fatalf("the set-aside log ends with %+v, want kapi's forgotten end", last)
	}
	if resp := call(t, a.home, &Request{Op: "open", Page: path}); resp.Error != "" {
		t.Fatalf("opening the forgotten file again: %s", resp.Error)
	}
	if p := a.indexed(path); !p.ArchivedAt.IsZero() {
		t.Fatal("the reopened page came back archived")
	}
}

func TestAnExpiredDraftLeavesAnImagesFolderAnotherPageUses(t *testing.T) {
	a, library := expiring(t, "")
	path := a.draft(library, "shared.html", 48*time.Hour)
	other := filepath.Join(a.dir, "other.html")
	mustWrite(t, other, `<h1>Other</h1><figure><img src="`+filepath.ToSlash(filepath.Join(page.FilesDir(path), "a.png"))+`" alt=""></figure>`)
	if resp := call(t, a.home, &Request{Op: "open", Page: other}); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	a.archiveNow(path)
	a.sweepAfter(deleteAfter)
	missing(t, path)
	if !a.inTrash("shared.html") {
		t.Fatal("the draft is not in the Trash")
	}
	if a.inTrash("shared.files") || mustRead(t, filepath.Join(page.FilesDir(path), "a.png")) != "png" {
		t.Fatal("the sweep trashed an images folder another page shows")
	}
}

func TestTheLastMomentCheckKeepsAPageBroughtBack(t *testing.T) {
	cases := []struct {
		name  string
		bring func(a *archiveSite, path, pageURL string)
	}{
		{"a tab opens", func(a *archiveSite, path, pageURL string) {
			connectViewer(a.t, pageURL)
			waitUntil(a.t, "the tab to count", func() bool { return a.s.inUse(page.ID(path), path) })
		}},
		{"a human touches it", func(a *archiveSite, path, _ string) {
			st, _, err := a.s.store(path)
			if err != nil {
				a.t.Fatal(err)
			}
			a.clock.add(time.Second)
			if err := st.Touch(); err != nil {
				a.t.Fatal(err)
			}
		}},
		{"the person saves it", func(a *archiveSite, path, _ string) {
			st, _, err := a.s.store(path)
			if err != nil {
				a.t.Fatal(err)
			}
			if err := st.SetSaved(true); err != nil {
				a.t.Fatal(err)
			}
		}},
		{"an agent edits the file", func(a *archiveSite, path, _ string) {
			mustWrite(a.t, path, "<h1>still needed</h1>\n")
		}},
		{"an agent attaches", func(a *archiveSite, path, _ string) {
			a.s.setAttached(path, "agent", true)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, library := expiring(t, "")
			path := a.draft(library, "late.html", 48*time.Hour)
			pageURL := call(t, a.home, &Request{Op: "open", Page: path}).URL
			a.archiveNow(path)
			a.clock.add(deleteAfter)
			d := pageDir{path: path, dir: filepath.Join(a.home, "pages", page.ID(path))}
			at, _ := stampOf(path)
			lc, _ := lifecycleOf(d.dir, at.mod)
			p, _ := a.s.policy()
			tc.bring(a, path, pageURL)
			removed, err := a.s.expire(d, &lc, at, p)
			if err != nil {
				t.Fatal(err)
			}
			if removed || !a.known(path) || a.inTrash("late.html") {
				t.Fatal("the page was removed although it was brought back after the sweep checked it")
			}
			if _, _, err := a.s.store(path); err != nil {
				t.Fatalf("the kept page cannot be opened: %v", err)
			}
		})
	}
}

func TestUnreadableSettingsStopDeletion(t *testing.T) {
	a, library := expiring(t, "")
	path := a.draft(library, "old.html", 48*time.Hour)
	a.archiveNow(path)
	mustWrite(t, filepath.Join(a.home, "settings.json"), `{"retention":{"deleteAfter":"0"}},`)
	a.sweepAfter(deleteAfter)
	if !a.known(path) || a.inTrash("old.html") {
		t.Fatal("settings.json cannot be read, but the sweep deleted a page with the default timer")
	}
}

func TestTheSweepRemovesOldSaveRedirects(t *testing.T) {
	for _, tc := range []struct {
		retention string
		keep      time.Duration
	}{{"", deleteAfter}, {`{"deleteAfter":"0"}`, redirectsWithoutDeletion}} {
		a, _ := expiring(t, tc.retention)
		redirect := func(name string, age time.Duration, extra ...string) string {
			dir := filepath.Join(a.home, "pages", name)
			mustWrite(t, filepath.Join(dir, movedName), "/elsewhere/"+name+".html\n")
			for _, f := range extra {
				mustWrite(t, filepath.Join(dir, f), "")
			}
			at := a.clock.now().Add(-age)
			if err := os.Chtimes(filepath.Join(dir, movedName), at, at); err != nil {
				t.Fatal(err)
			}
			return dir
		}
		old := redirect("0000000000000001", tc.keep)
		young := redirect("0000000000000002", tc.keep-time.Minute)
		reused := redirect("0000000000000003", tc.keep, "page.txt")
		journal := redirect("0000000000000004", tc.keep, intentName)
		a.sweepAfter(0)
		missing(t, old)
		for _, dir := range []string{young, reused, journal} {
			if _, err := os.Stat(filepath.Join(dir, movedName)); err != nil {
				t.Errorf("deleteAfter %q: the sweep removed %s: %v", tc.retention, filepath.Base(dir), err)
			}
		}
	}
}
