package daemon

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/trash"
	"github.com/xaaha/kapi/internal/web/app"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func missing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is still there (err %v)", path, err)
	}
}

func indexed(t *testing.T, s *Server, id string) bool {
	t.Helper()
	pages, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(pages, func(p app.Page) bool { return p.ID == id })
}

func noEnd(t *testing.T, s *Server, path string) {
	t.Helper()
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := l.After(0)
	if slices.ContainsFunc(events, func(e eventlog.Event) bool { return e.Kind == eventlog.KindEnd }) {
		t.Fatalf("a failed delete wrote an end: %+v", events)
	}
}

func openWithImage(t *testing.T, s *Server, dir string) (path, id string) {
	t.Helper()
	path = filepath.Join(dir, "plan.html")
	mustWrite(t, path, "<h1>Rollout plan</h1><p>x</p>")
	mustWrite(t, filepath.Join(page.FilesDir(path), "a.png"), "png")
	if _, _, err := s.store(path); err != nil {
		t.Fatal(err)
	}
	return path, page.ID(path)
}

func TestDeleteTrashesThePageAndItsImagesAndUndoBringsEverythingBack(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "keep this"}); err != nil {
		t.Fatal(err)
	}
	src := mustRead(t, path)
	got, err := s.Delete(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Title != "Rollout plan" {
		t.Fatalf("Delete = %+v", got)
	}
	missing(t, path)
	missing(t, page.FilesDir(path))
	missing(t, filepath.Join(home, "pages", id))
	if mustRead(t, filepath.Join(home, "trash", "files", "plan.html")) != src {
		t.Fatal("the page is not in the trash")
	}
	if mustRead(t, filepath.Join(home, "trash", "files", "plan.files", "a.png")) != "png" {
		t.Fatal("the images folder is not in the trash")
	}
	if indexed(t, s, id) {
		t.Fatal("home still lists the deleted page")
	}
	if _, err := s.page(path); !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "was deleted") {
		t.Fatalf("opening the deleted page: %v", err)
	}
	if resp := call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "hi"}); !strings.Contains(resp.Error, "was deleted and is in the Trash") {
		t.Fatalf("an agent message to the deleted page: %+v", resp)
	}

	back, err := s.Restore(id)
	if err != nil {
		t.Fatal(err)
	}
	if back != got {
		t.Fatalf("Restore = %+v, want %+v", back, got)
	}
	if mustRead(t, path) != src || mustRead(t, filepath.Join(page.FilesDir(path), "a.png")) != "png" {
		t.Fatal("the page or its image did not come back")
	}
	missing(t, filepath.Join(home, "deleted", id))
	if !indexed(t, s, id) {
		t.Fatal("home does not list the restored page")
	}
	l, err = s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := l.After(0)
	if !slices.ContainsFunc(events, func(e eventlog.Event) bool { return e.Text == "keep this" }) {
		t.Fatalf("the restored page lost its history: %+v", events)
	}
	last := events[len(events)-1]
	if last.Kind != eventlog.KindEnd || last.Text != "" || l.Ended() != last.Seq {
		t.Fatalf("the restored page ends with %+v, want a plain end in force", last)
	}
}

func TestDeleteWakesAWaitingAgentWithAnEndThatSaysThePageWasDeleted(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "wait", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	waitPresence(t, l, "agent watching")
	if _, err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var got Response
	if err := json.NewDecoder(conn).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if n := len(got.Events); n == 0 || got.Events[0].Kind != eventlog.KindEnd || got.Events[0].Text != eventlog.EndDeleted {
		t.Fatalf("wait returned %+v, want the deleted end first", got.Events)
	}
}

func TestDeleteOfAPageWhoseFileIsAlreadyGoneSetsItsStateAside(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	missing(t, filepath.Join(home, "pages", id))
	missing(t, page.FilesDir(path))
	if _, err := s.Restore(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "pages", id, "events.ndjson")); err != nil {
		t.Fatalf("the session did not come back: %v", err)
	}
	if mustRead(t, filepath.Join(page.FilesDir(path), "a.png")) != "png" {
		t.Fatal("the images did not come back")
	}
}

func TestDeleteTrashesAPageInsideAGitWorkTree(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	_, s, _ := serving(t)
	repo := t.TempDir()
	if out, err := exec.Command(git, "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	path, id := openWithImage(t, s, filepath.Join(repo, "docs"))
	if _, err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	missing(t, path)
	missing(t, page.FilesDir(path))
	if _, err := s.Restore(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestADeleteTheTrashCannotTakeLeavesThePageWhereItWas(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	blocked := filepath.Join(t.TempDir(), "trash")
	mustWrite(t, filepath.Join(blocked, "files"), "a file where the folder should be")
	s.Trash = trash.Folder(blocked)
	if _, err := s.Delete(id); err == nil || !strings.Contains(err.Error(), "could not move") {
		t.Fatalf("Delete = %v, want a trash failure", err)
	}
	if mustRead(t, path) == "" || mustRead(t, filepath.Join(page.FilesDir(path), "a.png")) != "png" {
		t.Fatal("the page or its images moved")
	}
	missing(t, filepath.Join(home, "deleted", id))
	if !indexed(t, s, id) {
		t.Fatal("home lost the page after a failed delete")
	}
	noEnd(t, s, path)
}

func TestAFailedImagesTrashPutsThePageFileBack(t *testing.T) {
	_, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	bin := t.TempDir()
	mustWrite(t, filepath.Join(bin, "info", "plan.files.trashinfo"), "taken")
	for n := 2; n <= 1000; n++ {
		mustWrite(t, filepath.Join(bin, "info", "plan "+strconv.Itoa(n)+".files.trashinfo"), "taken")
	}
	s.Trash = trash.Folder(bin)
	if _, err := s.Delete(id); err == nil {
		t.Fatal("Delete succeeded with no room for the images")
	}
	if mustRead(t, path) == "" {
		t.Fatal("the page file was not put back")
	}
	missing(t, filepath.Join(bin, "files", "plan.html"))
	noEnd(t, s, path)
}

func TestAFailedDeleteNeverWakesAWaitingAgent(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "wait", Page: path, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	waitPresence(t, l, "agent watching")
	blocked := filepath.Join(t.TempDir(), "trash")
	mustWrite(t, filepath.Join(blocked, "files"), "a file where the folder should be")
	s.Trash = trash.Folder(blocked)
	if _, err := s.Delete(id); err == nil {
		t.Fatal("Delete succeeded into a blocked trash")
	}
	silent(t, conn)
}

func TestAPageCallDuringRestoreNeitherStartsANewSessionNorLosesTheHistory(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	l, err := s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(&eventlog.Event{Author: "human", Kind: "message", Text: "keep this"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	var raced error
	restoredFiles = func(p string) { _, raced = s.page(p) }
	t.Cleanup(func() { restoredFiles = func(string) {} })
	if _, err := s.Restore(id); err != nil {
		t.Fatalf("Restore with a racing page call: %v", err)
	}
	if !errors.Is(raced, fs.ErrNotExist) {
		t.Fatalf("a page call during the restore = %v, want it refused", raced)
	}
	missing(t, filepath.Join(home, "deleted", id))
	l, err = s.page(path)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := l.After(0)
	if !slices.ContainsFunc(events, func(e eventlog.Event) bool { return e.Text == "keep this" }) {
		t.Fatalf("the restored page lost its history: %+v", events)
	}
}

func TestRestoreNeverReplacesANewFileAtThePath(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	if _, err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, "new")
	if _, err := s.Restore(id); !errors.Is(err, trash.ErrOccupied) {
		t.Fatalf("Restore over a new file = %v, want ErrOccupied", err)
	}
	if mustRead(t, path) != "new" {
		t.Fatal("the new file was replaced")
	}
	missing(t, page.FilesDir(path))
	if _, err := os.Stat(filepath.Join(home, "deleted", id, "state")); err != nil {
		t.Fatalf("the set-aside state is gone after a refused restore: %v", err)
	}
}

func TestSetAsideStateIsKeptThroughTheUndoWindowAndThenSwept(t *testing.T) {
	home, s, _ := serving(t)
	path, id := openWithImage(t, s, t.TempDir())
	if _, err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	s.sweepDeleted(time.Now().Add(keepDeleted - time.Minute))
	if _, err := os.Stat(filepath.Join(home, "deleted", id)); err != nil {
		t.Fatalf("swept inside the undo window: %v", err)
	}
	s.sweepDeleted(time.Now().Add(keepDeleted + time.Minute))
	missing(t, filepath.Join(home, "deleted", id))
	if _, err := s.Restore(id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Restore after the sweep = %v, want ErrNotExist", err)
	}
	if mustRead(t, filepath.Join(home, "trash", "files", "plan.html")) == "" {
		t.Fatal("the sweep touched the file in the Trash")
	}
	if _, err := s.page(path); err != nil {
		t.Fatalf("after the sweep the path does not open as a new page: %v", err)
	}
}

func TestDeleteAndRestoreOfAnUnknownPageAreNotFound(t *testing.T) {
	_, s, _ := serving(t)
	for _, id := range []string{"0123456789abcdef", "../../etc", ""} {
		if _, err := s.Delete(id); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Delete(%q) = %v", id, err)
		}
		if _, err := s.Restore(id); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Restore(%q) = %v", id, err)
		}
	}
}

func TestDeleteKeepsAnImagesFolderAnotherPageUsesAndUndoStillWorks(t *testing.T) {
	_, s, _ := serving(t)
	dir := t.TempDir()
	path, id := openWithImage(t, s, dir)
	other := filepath.Join(dir, "summary.html")
	mustWrite(t, other, `<h1>Summary</h1><p><img srcset="plan.files/a.png 2x" alt=""></p>`)
	if _, _, err := s.store(other); err != nil {
		t.Fatal(err)
	}
	got, err := s.Delete(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.KeptImages {
		t.Fatalf("Delete = %+v, want it to say another page still uses the images", got)
	}
	missing(t, path)
	if mustRead(t, filepath.Join(page.FilesDir(path), "a.png")) != "png" {
		t.Fatal("the shared images folder went to the Trash")
	}
	back, err := s.Restore(id)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != id {
		t.Fatalf("Restore = %+v", back)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the page did not come back: %v", err)
	}
	if mustRead(t, filepath.Join(page.FilesDir(path), "a.png")) != "png" {
		t.Fatal("the shared images folder changed during the undo")
	}
}

func TestDeleteTrashesTheImagesFolderWhenNoOtherPageUsesIt(t *testing.T) {
	home, s, _ := serving(t)
	dir := t.TempDir()
	path, id := openWithImage(t, s, dir)
	other := filepath.Join(dir, "summary.html")
	mustWrite(t, other, `<h1>Summary</h1><p><img src="summary.files/b.png" alt=""><a href="plan.html">plan</a></p>`)
	if _, _, err := s.store(other); err != nil {
		t.Fatal(err)
	}
	got, err := s.Delete(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.KeptImages {
		t.Fatalf("Delete = %+v, want the images trashed", got)
	}
	missing(t, page.FilesDir(path))
	if mustRead(t, filepath.Join(home, "trash", "files", "plan.files", "a.png")) != "png" {
		t.Fatal("the images are not in the trash")
	}
}
