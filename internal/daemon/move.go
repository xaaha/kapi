package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xaaha/kapi/internal/page"
)

const (
	stepBegun   = "begun"
	stepPlaced  = "placed"
	stepImages  = "images"
	stepRemoved = "removed"
	stepCleanup = "cleanup"

	imagesRename = "rename"
	imagesCopy   = "copy"
)

var ErrSourceChanged = errors.New("the page changed while kapi was saving it, so nothing moved; save it again")

var (
	linkFile    = os.Link
	renamePath  = os.Rename
	renameState = os.Rename
	removePath  = os.Remove
	removeTree  = os.RemoveAll
)

type moveJournal struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Step    string `json:"step"`
	Images  string `json:"images,omitempty"`
	Rewrite bool   `json:"rewrite,omitempty"`
	Placed  string `json:"placed"`

	dir string
}

func (j *moveJournal) save() error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(j.dir, intentName), b)
}

func (j *moveJournal) advance(step string) error {
	j.Step = step
	return j.save()
}

func (j *moveJournal) drop() error {
	return removeMissing(filepath.Join(j.dir, intentName))
}

func loadJournal(dir string) (*moveJournal, error) {
	b, err := os.ReadFile(filepath.Join(dir, intentName))
	if err != nil {
		return nil, err
	}
	var j moveJournal
	if err := json.Unmarshal(b, &j); err != nil || !filepath.IsAbs(j.From) || !filepath.IsAbs(j.To) {
		return nil, fmt.Errorf("unreadable %s: %s", filepath.Join(dir, intentName), b)
	}
	j.dir = dir
	return &j, nil
}

func present(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Server) move(from, dir string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.resumePending(from); err != nil {
		return "", err
	}
	if to, moved := s.movedTo(from); moved && !present(from) {
		return "", &page.MovedError{From: from, To: to}
	}
	info, err := os.Stat(from)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", from)
	}
	if resolved(filepath.Dir(from)) == resolved(dir) {
		if st := s.stores[from]; st != nil {
			return from, st.SetSaved(true)
		}
		return from, page.MarkSaved(s.stateDir(from))
	}
	if st := s.stores[from]; st != nil {
		if err := st.Retire(); err != nil {
			return "", err
		}
		delete(s.stores, from)
	}
	if err := os.MkdirAll(s.stateDir(from), 0o700); err != nil {
		return "", err
	}
	var j *moveJournal
	for n := 1; ; n++ {
		to := filepath.Join(dir, page.Numbered(filepath.Base(from), n))
		if s.taken(to) {
			continue
		}
		if j, err = s.beginMove(from, to); err != nil {
			return "", err
		}
		err = s.runMove(j, info)
		if err == nil {
			break
		}
		if undoErr := s.undoMove(j); undoErr != nil {
			s.pend(j)
			return "", errors.Join(err, fmt.Errorf("kapi could not put everything back yet and will retry: %w", undoErr))
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	if l := s.pages[from]; l != nil {
		if err := l.Retire(j.To); err != nil {
			log.Printf("close the log of %s: %v", from, err)
		}
		delete(s.pages, from)
	}
	t := &s.tabs
	t.mu.Lock()
	if at, ok := t.opened[from]; ok {
		t.opened[j.To] = at
		delete(t.opened, from)
	}
	t.mu.Unlock()
	if err := s.finishMove(j); err != nil {
		if err = s.finishMove(j); err != nil {
			s.pend(j)
			return j.To, fmt.Errorf("the page is saved at %s, but kapi could not move its history yet and will retry: %w", j.To, err)
		}
	}
	return j.To, nil
}

func (s *Server) taken(path string) bool {
	return present(path) || present(page.FilesDir(path)) || present(s.stateDir(path))
}

func (s *Server) beginMove(from, to string) (*moveJournal, error) {
	src, err := os.ReadFile(from)
	if err != nil {
		return nil, err
	}
	j := &moveJournal{From: from, To: to, Step: stepBegun, dir: s.stateDir(from)}
	if fi, err := os.Stat(page.FilesDir(from)); err == nil && fi.IsDir() {
		j.Images = imagesRename
		j.Rewrite = filepath.Base(page.FilesDir(from)) != filepath.Base(page.FilesDir(to))
	}
	if j.Rewrite {
		src = page.RenameImageRefs(src, from, to)
	}
	j.Placed = sum(src)
	if err := j.save(); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Server) runMove(j *moveJournal, info os.FileInfo) error {
	if err := placeFile(j); err != nil {
		return err
	}
	if err := moveImages(j); err != nil {
		return err
	}
	if err := checkSource(j.From, info); err != nil {
		return err
	}
	if err := removePath(j.From); err != nil {
		return err
	}
	if err := j.advance(stepRemoved); err != nil {
		log.Printf("record the save of %s: %v", j.From, err)
	}
	return nil
}

func placeFile(j *moveJournal) error {
	src, err := os.ReadFile(j.From)
	if err != nil {
		return err
	}
	if j.Rewrite {
		src = page.RenameImageRefs(src, j.From, j.To)
	}
	if sum(src) != j.Placed {
		return ErrSourceChanged
	}
	if j.Rewrite {
		err = writeNewFile(j.To, src, j.From)
	} else if err = linkFile(j.From, j.To); err != nil && !errors.Is(err, fs.ErrExist) {
		err = writeNewFile(j.To, src, j.From)
	}
	if err != nil {
		return err
	}
	return j.advance(stepPlaced)
}

func moveImages(j *moveJournal) error {
	if j.Images == "" {
		return nil
	}
	filesFrom, filesTo := page.FilesDir(j.From), page.FilesDir(j.To)
	if j.Images == imagesRename {
		if present(filesTo) {
			return fs.ErrExist
		}
		if renamePath(filesFrom, filesTo) == nil {
			return j.advance(stepImages)
		}
		if err := os.Mkdir(filesTo, 0o755); err != nil { //nolint:gosec // an images folder next to the page
			return err
		}
		j.Images = imagesCopy
		if err := j.save(); err != nil {
			return err
		}
	}
	if err := copyDir(filesFrom, filesTo); err != nil {
		return err
	}
	return j.advance(stepImages)
}

func checkSource(from string, was os.FileInfo) error {
	now, err := os.Stat(from)
	if err != nil {
		return err
	}
	if !os.SameFile(now, was) || now.Size() != was.Size() || !now.ModTime().Equal(was.ModTime()) {
		return ErrSourceChanged
	}
	return nil
}

func (s *Server) undoMove(j *moveJournal) error {
	filesFrom, filesTo := page.FilesDir(j.From), page.FilesDir(j.To)
	switch j.Images {
	case imagesRename:
		if present(filesTo) && !present(filesFrom) {
			if err := renamePath(filesTo, filesFrom); err != nil {
				return err
			}
		} else if present(filesTo) && present(filesFrom) {
			if err := removeMissing(filesTo); err != nil && !errors.Is(err, fs.ErrExist) {
				log.Printf("leave the empty %s: %v", filesTo, err)
			}
		}
	case imagesCopy:
		if present(filesFrom) {
			if err := removeTree(filesTo); err != nil {
				return err
			}
		}
	}
	if ours, err := placedByUs(j); err != nil {
		return err
	} else if ours {
		if err := removePath(j.To); err != nil {
			return err
		}
	}
	if err := j.drop(); err != nil {
		return err
	}
	s.unpend(j)
	return nil
}

func placedByUs(j *moveJournal) (bool, error) {
	to, err := os.Stat(j.To)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if from, err := os.Stat(j.From); err == nil && os.SameFile(from, to) {
		return true, nil
	}
	b, err := os.ReadFile(j.To)
	if err != nil {
		return false, err
	}
	return sum(b) == j.Placed, nil
}

func (s *Server) finishMove(j *moveJournal) error {
	oldDir, newDir := s.stateDir(j.From), s.stateDir(j.To)
	if j.dir == oldDir {
		if present(newDir) {
			if err := setAside(newDir); err != nil {
				return err
			}
		}
		if err := renameState(oldDir, newDir); err != nil {
			return err
		}
		j.dir = newDir
	}
	if err := writeAtomic(filepath.Join(newDir, "page.txt"), []byte(j.To+"\n")); err != nil {
		return err
	}
	if err := page.MarkSaved(newDir); err != nil {
		return err
	}
	if err := removeMissing(filepath.Join(newDir, viewingName)); err != nil {
		return err
	}
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(oldDir, movedName), []byte(j.To+"\n")); err != nil {
		return err
	}
	s.unpend(j)
	if j.Images == imagesCopy && present(page.FilesDir(j.From)) {
		if err := j.advance(stepCleanup); err != nil {
			return err
		}
		return cleanUp(j)
	}
	return j.drop()
}

func cleanUp(j *moveJournal) error {
	if err := removeTree(page.FilesDir(j.From)); err != nil {
		log.Printf("remove the images folder %s left behind by the save of %s: %v; kapi retries on its next start", page.FilesDir(j.From), j.From, err)
		return nil
	}
	return j.drop()
}

func setAside(dir string) error {
	aside := dir + ".replaced-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := renameState(dir, aside); err != nil {
		return err
	}
	return removeMissing(filepath.Join(aside, "page.txt"))
}

func (s *Server) pend(j *moveJournal) {
	if s.pending == nil {
		s.pending = map[string]*moveJournal{}
	}
	s.pending[j.From], s.pending[j.To] = j, j
}

func (s *Server) unpend(j *moveJournal) {
	if s.pending[j.From] == j {
		delete(s.pending, j.From)
	}
	if s.pending[j.To] == j {
		delete(s.pending, j.To)
	}
}

func (s *Server) resumePending(path string) error {
	j, ok := s.pending[path]
	if !ok {
		return nil
	}
	if err := s.resume(j); err != nil {
		return fmt.Errorf("%s is still being saved from %s to %s and kapi could not finish it: %w; try again", path, j.From, j.To, err)
	}
	return nil
}

func (s *Server) resume(j *moveJournal) error {
	switch {
	case j.Step == stepCleanup:
		return cleanUp(j)
	case j.Step == stepRemoved || j.dir == s.stateDir(j.To):
		return s.finishMove(j)
	case present(j.From):
		return s.undoMove(j)
	case !present(j.To):
		s.unpend(j)
		return j.drop()
	}
	if err := redoImages(j); err != nil {
		return err
	}
	if err := j.advance(stepRemoved); err != nil {
		return err
	}
	return s.finishMove(j)
}

func redoImages(j *moveJournal) error {
	filesFrom, filesTo := page.FilesDir(j.From), page.FilesDir(j.To)
	if j.Images == "" || j.Step == stepImages || !present(filesFrom) {
		return nil
	}
	if j.Images == imagesRename && !present(filesTo) {
		return renamePath(filesFrom, filesTo)
	}
	if err := removeTree(filesTo); err != nil {
		return err
	}
	if err := os.Mkdir(filesTo, 0o755); err != nil { //nolint:gosec // an images folder next to the page
		return err
	}
	j.Images = imagesCopy
	return copyDir(filesFrom, filesTo)
}

func (s *Server) recoverMoves() {
	dirs, err := os.ReadDir(filepath.Join(s.home, "pages"))
	if err != nil {
		return
	}
	for _, d := range dirs {
		dir := filepath.Join(s.home, "pages", d.Name())
		if !present(filepath.Join(dir, intentName)) {
			continue
		}
		j, err := loadJournal(dir)
		if err != nil {
			log.Printf("move %s: %v", d.Name(), err)
			continue
		}
		if err := s.resume(j); err != nil {
			log.Printf("finish saving %s to %s: %v", j.From, j.To, err)
			if j.Step != stepCleanup {
				s.pend(j)
			}
		}
	}
}

func writeNewFile(path string, data []byte, like string) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(like); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := filepath.Join(filepath.Dir(path), ".~"+filepath.Base(path)+".saving")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err == nil {
		err = linkFile(tmp, path)
	}
	return errors.Join(err, os.Remove(tmp))
}

func copyDir(from, to string) error {
	root, err := os.OpenRoot(from)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(to, filepath.FromSlash(rel))
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			return os.Mkdir(dst, 0o755) //nolint:gosec // an images folder next to the page
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := root.ReadFile(rel)
		if err != nil {
			return err
		}
		return writeNewFile(dst, data, filepath.Join(from, filepath.FromSlash(rel)))
	})
}
