package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/settings"
)

func withLibrary(t *testing.T, s *Server) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "kapi")
	if _, err := s.prefs.Update(context.Background(), settings.Patch{Library: &root}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "library")
}

func draftWithImage(t *testing.T) (path string, img []byte) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "plan.html")
	img = []byte("\x89PNG fake")
	if err := os.Mkdir(filepath.Join(dir, "plan.files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.files", "a.png"), img, 0o600); err != nil {
		t.Fatal(err)
	}
	src := "<h1>Plan</h1>\n<figure><img src=\"./plan.files/a.png\" alt=\"a\"></figure>\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, img
}

func TestSaveCarriesTheFileImagesAndStateIntoTheLibrary(t *testing.T) {
	home, s, _ := serving(t)
	lib := withLibrary(t, s)
	from, img := draftWithImage(t)
	if r := call(t, home, &Request{Op: "open", Page: from, Dir: filepath.Dir(from)}); r.Error != "" {
		t.Fatal(r.Error)
	}
	call(t, home, &Request{Op: "append", Page: from, Author: "human", Kind: "message", Text: "keep this"})
	call(t, home, &Request{Op: "pending", Page: from, Reader: "agent"})
	st, _, err := s.store(from)
	if err != nil {
		t.Fatal(err)
	}
	_, rev := st.Source()
	oldDir := s.stateDir(from)

	to, err := s.Save(from, "Work")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(lib, "Work", "plan.html"); to != want {
		t.Fatalf("Save = %q, want %q", to, want)
	}
	if _, err := os.Lstat(from); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the draft is still there: %v", err)
	}
	if _, err := os.Lstat(page.FilesDir(from)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the draft's images folder is still there: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(lib, "Work", "plan.files", "a.png")); err != nil || !bytes.Equal(b, img) {
		t.Errorf("image after save = %q, %v", b, err)
	}
	newDir := s.stateDir(to)
	if b, _ := os.ReadFile(filepath.Join(newDir, "page.txt")); string(b) != to+"\n" {
		t.Errorf("page.txt = %q, want the new path", b)
	}
	if lc, err := page.ReadLifecycle(newDir); !lc.Saved || err != nil {
		t.Errorf("saved = %v, %v; want true", lc.Saved, err)
	}
	if b, _ := os.ReadFile(filepath.Join(oldDir, movedName)); string(b) != to+"\n" {
		t.Errorf("old state dir moved.txt = %q", b)
	}
	if ents, _ := os.ReadDir(oldDir); len(ents) != 1 {
		t.Errorf("old state dir holds %v, want only %s", ents, movedName)
	}
	if _, err := os.Stat(filepath.Join(newDir, intentName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s left behind: %v", intentName, err)
	}

	resp := call(t, home, &Request{Op: "open", Page: to})
	if resp.Error != "" || !strings.HasSuffix(resp.URL, "p/"+page.ID(to)+"/") {
		t.Fatalf("open new path = %+v", resp)
	}
	moved, _, err := s.store(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, now := moved.Source(); now != rev {
		t.Errorf("revision after save = %d, want %d carried over", now, rev)
	}
	if !moved.Saved() {
		t.Error("the reopened store does not know it is saved")
	}
	pending := call(t, home, &Request{Op: "pending", Page: to, Reader: "agent"})
	if len(pending.Events) != 0 {
		t.Errorf("pending after save = %+v, want the agent's cursor carried over", pending.Events)
	}
	status := call(t, home, &Request{Op: "status", Page: to})
	if status.Error != "" {
		t.Fatal(status.Error)
	}
	l, err := s.page(to)
	if err != nil {
		t.Fatal(err)
	}
	if events, _ := l.After(0); !slices.ContainsFunc(events, func(e eventlog.Event) bool { return e.Text == "keep this" }) {
		t.Errorf("events after save = %+v, want the human's message carried over", events)
	}
	if dirs := s.openers(to); !slices.Contains(dirs, resolved(filepath.Dir(from))) {
		t.Errorf("openers after save = %v, want the folder it was opened from", dirs)
	}

	index, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 || index[0].Path != to || !index[0].Saved || index[0].SavedIn != Tilde(filepath.Join(lib, "Work")) {
		t.Errorf("index = %+v, want one saved page in Work", index)
	}
}

func TestSaveNeverOverwritesAndPointsImagesAtTheRenamedFolder(t *testing.T) {
	home, s, _ := serving(t)
	lib := withLibrary(t, s)
	work := filepath.Join(lib, "Work")
	if err := os.MkdirAll(filepath.Join(work, "plan 2.files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "plan.html"), []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	from, img := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})

	to, err := s.Save(from, "Work")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(work, "plan 3.html"); to != want {
		t.Fatalf("Save = %q, want %q", to, want)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "plan.html")); string(b) != "theirs" {
		t.Errorf("existing plan.html = %q, want it untouched", b)
	}
	if ents, _ := os.ReadDir(filepath.Join(work, "plan 2.files")); len(ents) != 0 {
		t.Errorf("existing plan 2.files gained %v", ents)
	}
	if b, err := os.ReadFile(filepath.Join(work, "plan 3.files", "a.png")); err != nil || !bytes.Equal(b, img) {
		t.Errorf("image = %q, %v", b, err)
	}
	src, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `src="./plan%203.files/a.png"`) {
		t.Errorf("saved page does not point at its renamed images folder:\n%s", src)
	}
	st, _, err := s.store(to)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := st.Source()
	if !strings.Contains(string(cur), "plan%203.files/a.png") {
		t.Errorf("store source does not hold the new image path:\n%s", cur)
	}
}

func TestWaitOnTheOldPathExitsNamingTheNewOne(t *testing.T) {
	home, s, _ := serving(t)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	l, err := s.page(from)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", SocketPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "wait", Page: from, Reader: "agent"}); err != nil {
		t.Fatal(err)
	}
	watcher := attachWatcher(t, home, from)
	waitPresence(t, l, "agent watching")

	to, err := s.Save(from, "Work")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []net.Conn{conn, watcher} {
		if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var resp Response
		if err := json.NewDecoder(c).Decode(&resp); err != nil {
			t.Fatalf("no reply to a waiting agent after the save: %v", err)
		}
		if !strings.Contains(resp.Error, "moved to "+to) || !strings.Contains(resp.Error, "kapi wait") {
			t.Errorf("reply = %+v, want an error naming %s", resp, to)
		}
	}
	for _, op := range []string{"wait", "append", "pending", "open"} {
		resp := call(t, home, &Request{Op: op, Page: from, Reader: "agent", Author: "agent", Kind: "message", Text: "late"})
		if !strings.Contains(resp.Error, from+" moved to "+to) {
			t.Errorf("%s on the old path = %+v, want an error naming the new path", op, resp)
		}
	}
	if _, err := os.Stat(filepath.Join(s.stateDir(from), "events.ndjson")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a late request recreated the old log: %v", err)
	}
}

func TestATabOnTheOldAddressFollowsThePage(t *testing.T) {
	home, s, _ := serving(t)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	oldURL := call(t, home, &Request{Op: "open", Page: from}).URL
	req, err := http.NewRequest(http.MethodGet, oldURL+"events", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	lines := bufio.NewScanner(stream.Body)
	lines.Scan()

	to, err := s.Save(from, "Work")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for lines.Scan() {
		got = append(got, lines.Text())
	}
	if !slices.Contains(got, "event: moved") || !slices.Contains(got, "data: "+page.ID(to)) {
		t.Errorf("stream ended with %q, want a moved event naming %s", got, page.ID(to))
	}

	follow, err := http.NewRequest(http.MethodGet, oldURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(follow)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasSuffix(resp.Request.URL.Path, "/p/"+page.ID(to)+"/") {
		t.Errorf("old address answered %s at %s, want the page at its new id", resp.Status, resp.Request.URL)
	}
}

func TestUnsaveKeepsTheFileAndRestartsTheClock(t *testing.T) {
	home, s, _ := serving(t)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	to, err := s.Save(from, "Work")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := s.Unsave(to); err != nil {
		t.Fatal(err)
	}
	if lc, _ := page.ReadLifecycle(s.stateDir(to)); lc.Saved {
		t.Error("still saved after Unsave")
	}
	if _, err := os.Stat(to); err != nil {
		t.Errorf("Unsave moved the file: %v", err)
	}
	if lc, _ := page.ReadLifecycle(s.stateDir(to)); lc.Touched.Before(before) {
		t.Errorf("touched %v after unsave, want no earlier than the unsave at %v", lc.Touched, before)
	}
}

func TestSavingIntoItsOwnFolderOnlyMarksIt(t *testing.T) {
	home, s, _ := serving(t)
	lib := withLibrary(t, s)
	dir := filepath.Join(lib, "Work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plan.html")
	if err := os.WriteFile(path, []byte("<p>x</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	call(t, home, &Request{Op: "open", Page: path})
	to, err := s.Save(path, "Work")
	if err != nil || to != path {
		t.Fatalf("Save = %q, %v; want the same path", to, err)
	}
	st, _, err := s.store(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Saved() {
		t.Error("not saved")
	}
}

func TestSaveRefusesBadFolderNames(t *testing.T) {
	_, s, _ := serving(t)
	lib := withLibrary(t, s)
	from, _ := draftWithImage(t)
	for _, bad := range []string{"", "  ", "..", "a/../b", ".hidden", "plan.files", "a\\b", "a/b/c/d/e"} {
		if _, err := s.Save(from, bad); !errors.Is(err, settings.ErrBadFolder) {
			t.Errorf("Save(%q) = %v, want ErrBadFolder", bad, err)
		}
	}
	if _, err := os.Stat(from); err != nil {
		t.Errorf("a refused save moved the page: %v", err)
	}
	if _, err := os.Stat(lib); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused save created the library: %v", err)
	}
}

func TestLibraryListsFoldersButNotImagesOrHiddenOnes(t *testing.T) {
	_, s, _ := serving(t)
	lib := withLibrary(t, s)
	if root, folders, err := s.Library(); err != nil || len(folders) != 0 || root != Tilde(lib) {
		t.Fatalf("empty library = %q %v %v", root, folders, err)
	}
	for _, d := range []string{"Work/Clients", "kapi", ".git", "Work/plan.files", "Work/Clients/A/B/C"} {
		if err := os.MkdirAll(filepath.Join(lib, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, folders, err := s.Library()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"kapi", "Work", "Work/Clients", "Work/Clients/A", "Work/Clients/A/B"}; !slices.Equal(folders, want) {
		t.Errorf("folders = %q, want %q", folders, want)
	}
}

func TestANewFileAtAMovedPathStartsAFreshPage(t *testing.T) {
	home, s, _ := serving(t)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	call(t, home, &Request{Op: "append", Page: from, Author: "human", Kind: "message", Text: "old"})
	if _, err := s.Save(from, "Work"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("<p>new</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := call(t, home, &Request{Op: "open", Page: from}); r.Error != "" {
		t.Fatalf("open of a new file at the old path: %s", r.Error)
	}
	if p := call(t, home, &Request{Op: "pending", Page: from, Reader: "agent"}); len(p.Events) != 0 {
		t.Errorf("the new page inherited %+v", p.Events)
	}
}
