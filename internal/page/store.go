package page

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/render"
)

var (
	ErrStale         = errors.New("page changed since that revision")
	ErrNotFound      = errors.New("no such element")
	ErrInboundLinks  = errors.New("other elements link to it")
	ErrNothingToUndo = errors.New("nothing to undo")
	ErrUndoConflict  = errors.New("the file changed outside the browser after that op")
	ErrDanglingLinks = errors.New("the edit leaves links to missing elements")
	ErrEditLostID    = errors.New("the edited element would no longer be on the page; its tag cannot stand there")
	ErrPresetInProse = errors.New("prose html cannot hold kapi-* elements other than kapi-badge, or data-kapi-preset; edit the preset's source instead")
	ErrInnerEscapes  = errors.New("the new source closes the element early or leaves an element inside it open; edit only what goes between its tags")
)

type Op struct {
	Kind   string  `json:"kind"`
	ID     string  `json:"id"`
	HTML   string  `json:"html,omitempty"`
	Inner  *string `json:"inner,omitempty"`
	Cells  []Cell  `json:"cells,omitempty"`
	Before string  `json:"before,omitempty"`
	Done   bool    `json:"done,omitempty"`
	Dir    string  `json:"dir,omitempty"`
}

type Cell struct {
	Row  int    `json:"row"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

type Summary struct {
	Text string
	IDs  []string
}

type Store struct {
	mu   sync.Mutex
	path string
	dir  string
	src  []byte
	rev  uint64
	undo []uint64

	conversion string
	held       *held
}

type state struct {
	Rev  uint64   `json:"rev"`
	Undo []uint64 `json:"undo"`
	Next *state   `json:"next,omitempty"`
	Temp string   `json:"temp,omitempty"`
	Held *held    `json:"held,omitempty"`
}

type held struct {
	Name   string    `json:"name"`
	Seen   fileStamp `json:"seen"`
	Placed fileStamp `json:"placed"`
	quiet  int
}

type fileStamp struct {
	Ino   uint64 `json:"ino"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
	Ctime int64  `json:"ctime"`
	Sum   string `json:"sum"`
}

const heldTicks = 3

// ID is the 16-hex directory name under $KAPI_HOME/pages for an absolute page path.
func ID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:8])
}

func Open(home, path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("page path must be absolute: %q", path)
	}
	s := &Store{path: path, dir: filepath.Join(home, "pages", ID(path))}
	if err := os.MkdirAll(filepath.Join(s.dir, "revs"), 0o700); err != nil {
		return nil, err
	}
	for _, dir := range []string{s.dir, filepath.Join(s.dir, "revs"), filepath.Join(s.dir, "raw")} {
		temps, err := findTemps(dir, "")
		if err != nil {
			return nil, err
		}
		for _, tmp := range temps {
			if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}
	var st state
	b, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &st); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(s.dir, "state.json"), err)
		}
	}
	if err := s.load(&st); err != nil {
		return nil, err
	}
	s.held = st.Held
	if err := s.checkHeld(); err != nil {
		return nil, err
	}
	if s.held != nil {
		if err := s.dropHeld(); err != nil {
			return nil, err
		}
	}
	if err := s.restoreDisplaced(&st); err != nil {
		return nil, err
	}
	if st.Next != nil {
		if err := s.saveState(state{Rev: s.rev, Undo: s.undo}); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		src, text, err := s.absorb(raw, s.rev+1)
		if err != nil {
			return nil, err
		}
		if s.rev > 0 && bytes.Equal(raw, s.src) && bytes.Equal(src, raw) {
			return s, nil
		}
		err = s.commit(src, raw, s.rev+1, s.undo)
		if errors.Is(err, errFileChanged) && attempt < 10 {
			continue
		}
		if errors.Is(err, errFileChanged) {
			return s, s.commit(raw, raw, s.rev+1, s.undo)
		}
		if err != nil {
			return nil, err
		}
		s.conversion = text
		return s, nil
	}
}

func (s *Store) load(st *state) error {
	s.rev, s.undo = st.Rev, st.Undo
	if s.rev > 0 {
		src, err := os.ReadFile(s.revPath(s.rev))
		if err != nil {
			return err
		}
		s.src = src
	}
	if st.Next == nil {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if after, err := os.ReadFile(s.revPath(st.Next.Rev)); err == nil && bytes.Equal(raw, after) {
		s.src, s.rev, s.undo = after, st.Next.Rev, st.Next.Undo
	}
	return nil
}

func (s *Store) restoreDisplaced(st *state) error {
	temps, err := findTemps(filepath.Dir(s.path), filepath.Base(s.path))
	if err != nil || len(temps) == 0 {
		return err
	}
	before, err := s.readRev(st.Rev)
	if err != nil {
		return err
	}
	var after []byte
	if st.Next != nil {
		if after, err = s.readRev(st.Next.Rev); err != nil {
			return err
		}
	}
	page, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	for _, tmp := range temps {
		theirs, err := os.ReadFile(tmp)
		if err != nil {
			return err
		}
		named := st.Next != nil && filepath.Base(tmp) == st.Next.Temp
		switch {
		case bytes.Equal(theirs, before) || (st.Next != nil && bytes.Equal(theirs, after)):
		case named && bytes.Equal(page, after):
			_, err = swapIn(tmp, s.path, theirs, page)
			if errors.Is(err, errFileChanged) {
				err = keep(tmp)
			}
			if err != nil {
				return err
			}
			if err := syncDir(filepath.Dir(s.path)); err != nil {
				return err
			}
		case named && bytes.Equal(page, before):
		default:
			if err := keep(tmp); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func keep(tmp string) error {
	dir, name := filepath.Split(tmp)
	i := strings.LastIndex(name, tempMark) + len(tempMark)
	kept := filepath.Join(dir, name[:i]+"kept-"+name[i:])
	for n := 2; ; n++ {
		if _, err := os.Lstat(kept); errors.Is(err, os.ErrNotExist) {
			return os.Rename(tmp, kept)
		}
		kept = filepath.Join(dir, name[:i]+"kept-"+name[i:]+"-"+strconv.Itoa(n))
	}
}

func (s *Store) readRev(rev uint64) ([]byte, error) {
	if rev == 0 {
		return nil, nil
	}
	return os.ReadFile(s.revPath(rev))
}

func (s *Store) Path() string { return s.path }

func (s *Store) Conversion() string { return s.conversion }

func (s *Store) rawPath(rev uint64) string {
	return filepath.Join(s.dir, "raw", strconv.FormatUint(rev, 10)+".html")
}

func (s *Store) absorb(raw []byte, rev uint64) ([]byte, string, error) {
	src, ch, err := prepare(raw, s.src)
	if err != nil || ch.splits == 0 {
		return src, "", err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "raw"), 0o700); err != nil {
		return nil, "", err
	}
	if err := writeAtomic(s.rawPath(rev), raw); err != nil {
		return nil, "", err
	}
	text := fmt.Sprintf("split %d block(s) so each has its own id", ch.splits)
	if !ch.dropped.Empty() {
		text += "; removed " + ch.dropped.String()
	}
	return src, text + "; raw input saved to " + s.rawPath(rev), nil
}

type change struct {
	splits  int
	dropped render.Report
}

func (c *change) rewrite(part []byte) ([]byte, error) {
	d, err := render.Dropped(part)
	if err != nil {
		return nil, err
	}
	c.splits++
	for name, n := range d.Elements {
		c.dropped.Elements[name] += n
	}
	c.dropped.Attributes += d.Attributes
	return render.Canonical(part)
}

func prepare(src, prev []byte) ([]byte, change, error) {
	ch := change{dropped: render.Report{Elements: map[string]int{}}}
	whole := false
	for range 4 {
		els := parse(src)
		need := map[int]bool{}
		var split []int
		for _, i := range units(els) {
			n, err := render.CountBlocks(src[els[i].start:els[i].end])
			if err != nil {
				return nil, ch, err
			}
			switch {
			case n > 1:
				split = append(split, i)
			case n == 1:
				need[i] = true
			}
		}
		if len(split) > 0 {
			for k := len(split) - 1; k >= 0; k-- {
				e := els[split[k]]
				c, err := ch.rewrite(src[e.start:e.end])
				if err != nil {
					return nil, ch, err
				}
				src = splice(src, e.start, e.end, string(c))
			}
			continue
		}
		src = assignIDs(src, els, need, prev)
		missing, err := render.Unlabeled(src)
		if err != nil || missing == 0 || whole {
			return src, ch, err
		}
		whole = true
		if src, err = ch.rewrite(src); err != nil {
			return nil, ch, err
		}
	}
	return src, ch, nil
}

func (s *Store) Source() ([]byte, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.src), s.rev
}

// Refresh picks up an edit made to the file since kapi last read or wrote it.
func (s *Store) Refresh() (Summary, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkHeld(); err != nil {
		return Summary{}, false, err
	}
	for attempt := 0; attempt < 10; attempt++ {
		raw, err := os.ReadFile(s.path)
		if err != nil || bytes.Equal(raw, s.src) {
			return Summary{}, false, err
		}
		old := s.src
		src, text, err := s.absorb(raw, s.rev+1)
		if err != nil {
			return Summary{}, false, err
		}
		err = s.commit(src, raw, s.rev+1, s.undo)
		if errors.Is(err, errFileChanged) {
			continue
		}
		if err != nil {
			return Summary{}, false, err
		}
		ids := changedBlocks(old, s.src)
		summary := "edited in the file"
		if len(ids) > 0 {
			summary += ": " + describeIDs(s.src, ids)
		}
		if text != "" {
			summary += "; " + text
		}
		return Summary{Text: summary, IDs: ids}, true, nil
	}
	return Summary{}, false, nil
}

func (s *Store) Apply(rev uint64, op *Op) (uint64, Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkHeld(); err != nil {
		return s.rev, Summary{}, err
	}
	if rev != s.rev && !s.blockUnchangedSince(rev, op) {
		return s.rev, Summary{}, ErrStale
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s.rev, Summary{}, err
	}
	if !bytes.Equal(raw, s.src) {
		return s.rev, Summary{}, ErrStale
	}
	if op.Kind == "undo" {
		n := len(s.undo)
		if n == 0 {
			return s.rev, Summary{}, ErrNothingToUndo
		}
		back := s.undo[n-1]
		after, err := os.ReadFile(s.revPath(back + 1))
		if err != nil {
			return s.rev, Summary{}, err
		}
		if !bytes.Equal(after, s.src) {
			return s.rev, Summary{}, ErrUndoConflict
		}
		src, err := os.ReadFile(s.revPath(back))
		if err != nil {
			return s.rev, Summary{}, err
		}
		if err := s.commit(src, raw, s.rev+1, s.undo[:n-1]); err != nil {
			return s.rev, Summary{}, stale(err)
		}
		return s.rev, Summary{Text: "undid back to revision " + strconv.FormatUint(back, 10)}, nil
	}
	src, sum, err := apply(s.src, op)
	if err == nil {
		var text string
		if src, text, err = s.absorb(src, s.rev+1); text != "" {
			sum.Text += "; " + text
		}
	}
	if err == nil && op.Kind == "edit" {
		if _, lost := render.Block(src, op.ID); errors.Is(lost, render.ErrNotFound) {
			err = ErrEditLostID
		}
	}
	if err != nil {
		return s.rev, Summary{}, err
	}
	if bytes.Equal(src, s.src) {
		return s.rev, Summary{}, nil
	}
	if err := s.commit(src, raw, s.rev+1, append(s.undo, s.rev)); err != nil {
		return s.rev, Summary{}, stale(err)
	}
	return s.rev, sum, nil
}

func stale(err error) error {
	if errors.Is(err, errFileChanged) {
		return ErrStale
	}
	return err
}

func (s *Store) blockUnchangedSince(rev uint64, op *Op) bool {
	switch op.Kind {
	case "edit", "cells", "archive", "unarchive", "delete", "check", "orient":
	default:
		return false
	}
	then, err := os.ReadFile(s.revPath(rev))
	if err != nil {
		return false
	}
	was, err := Outer(then, op.ID)
	if err != nil {
		return false
	}
	is, err := Outer(s.src, op.ID)
	return err == nil && bytes.Equal(was, is)
}

func (s *Store) revPath(rev uint64) string {
	return filepath.Join(s.dir, "revs", strconv.FormatUint(rev, 10)+".html")
}

var (
	errFileChanged = errors.New("the file changed while kapi was writing it")
	errNoSwap      = errors.New("this file system cannot swap files atomically")
	replaceHook    func(stage string)
	syncFile       = (*os.File).Sync
	renameFile     = os.Rename
	swapFiles      = exchangeFiles
	writeTemp      = (*os.File).Write
)

func (s *Store) commit(src, base []byte, rev uint64, undo []uint64) error {
	if !bytes.Equal(src, base) {
		quiet, err := settled(s.path, placement{b: base})
		if err != nil {
			return err
		}
		if !quiet {
			return errFileChanged
		}
	}
	if err := writeAtomic(s.revPath(rev), src); err != nil {
		return err
	}
	next := state{Rev: rev, Undo: undo}
	if !bytes.Equal(src, base) {
		if err := s.dropHeld(); err != nil {
			return err
		}
		now := state{Rev: s.rev, Undo: s.undo}
		var n [4]byte
		if _, err := rand.Read(n[:]); err != nil {
			return err
		}
		intent := state{Rev: rev, Undo: undo, Temp: "." + filepath.Base(s.path) + tempMark + strconv.FormatUint(uint64(binary.BigEndian.Uint32(n[:])), 10)}
		if err := s.saveState(state{Rev: s.rev, Undo: s.undo, Next: &intent}); err != nil {
			return err
		}
		h, err := replaceFileVia(filepath.Join(filepath.Dir(s.path), intent.Temp), s.path, src, base)
		s.held = h
		if err != nil {
			return errors.Join(err, s.saveState(now))
		}
	}
	s.src, s.rev, s.undo = src, rev, undo
	return s.saveState(next)
}

func (s *Store) saveState(st state) error {
	st.Held = s.held
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, "state.json"), b)
}

const tempMark = ".kapi-"

func findTemps(dir, base string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var temps []string
	for _, e := range ents {
		name, ok := strings.CutPrefix(e.Name(), ".")
		i := strings.LastIndex(name, tempMark)
		if !ok || i < 1 || (base != "" && name[:i] != base) {
			continue
		}
		if _, err := strconv.ParseUint(name[i+len(tempMark):], 10, 64); err == nil {
			temps = append(temps, filepath.Join(dir, e.Name()))
		}
	}
	return temps, nil
}

func writeAtomic(path string, b []byte) error {
	return replaceFile(path, b, nil)
}

func replaceFile(path string, b, expect []byte) error {
	_, err := replaceFileVia("", path, b, expect)
	return err
}

func replaceFileVia(tmp, path string, b, expect []byte) (*held, error) {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	var f *os.File
	var err error
	if tmp == "" {
		f, err = os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+tempMark+"*")
	} else {
		f, err = os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return nil, err
	}
	var h *held
	defer func() {
		if h == nil {
			os.Remove(f.Name())
		}
	}()
	if _, err := writeTemp(f, b); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return nil, err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if expect == nil {
		err = renameFile(f.Name(), path)
	} else {
		h, err = swapIn(f.Name(), path, b, expect)
	}
	if err != nil {
		return h, err
	}
	return h, syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return syncFile(d)
}

func swapIn(tmp, path string, ours, expect []byte) (*held, error) {
	mine, err := os.Stat(tmp)
	if err != nil {
		return nil, err
	}
	if replaceHook != nil {
		replaceHook("swap")
	}
	err = swapFiles(tmp, path)
	if errors.Is(err, errNoSwap) {
		cur, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(cur, expect) {
			return nil, errFileChanged
		}
		return nil, renameFile(tmp, path)
	}
	if err != nil {
		return nil, err
	}
	displaced, err := capture(tmp)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(displaced.b, expect) {
		return holding(tmp, path, displaced)
	}
	prev, placed := placement{b: expect}, placement{fi: mine, b: ours}
	for range 1 << 16 {
		seen, err := capture(tmp)
		if err != nil {
			return nil, err
		}
		atPath, err := stamp(path, mine, ours)
		if err != nil {
			return nil, err
		}
		atTmp, err := stamp(tmp, mine, ours)
		if err != nil {
			return nil, err
		}
		newer := atTmp.After(atPath)
		if atTmp.Equal(atPath) {
			if newer, err = tiedTmpIsNewer(tmp, path, prev, placed); err != nil {
				return nil, err
			}
		}
		if !newer {
			h, err := holding(tmp, path, seen)
			if err != nil {
				return nil, err
			}
			return h, errFileChanged
		}
		if replaceHook != nil {
			replaceHook("restore")
		}
		if err := swapFiles(tmp, path); err != nil {
			return nil, err
		}
		prev, placed = placed, seen
	}
	return nil, errFileChanged
}

func holding(tmp, path string, seen placement) (*held, error) {
	at, err := capture(path)
	if err != nil {
		return nil, err
	}
	return &held{Name: filepath.Base(tmp), Seen: seen.stamp(), Placed: at.stamp()}, nil
}

func (p placement) stamp() fileStamp {
	ino, ctime := inodeAndCtime(p.fi)
	sum := sha256.Sum256(p.b)
	return fileStamp{Ino: ino, Size: p.fi.Size(), Mtime: p.fi.ModTime().UnixNano(), Ctime: ctime, Sum: hex.EncodeToString(sum[:])}
}

func (f fileStamp) sameContent(o fileStamp) bool {
	f.Ctime, o.Ctime = 0, 0
	return f == o
}

func (s *Store) heldPath() string { return filepath.Join(filepath.Dir(s.path), s.held.Name) }

func (s *Store) checkHeld() error {
	if s.held == nil {
		return nil
	}
	now, err := capture(s.heldPath())
	if errors.Is(err, os.ErrNotExist) {
		s.held = nil
		return s.saveState(state{Rev: s.rev, Undo: s.undo})
	}
	if err != nil {
		return err
	}
	if now.stamp() == s.held.Seen {
		if s.held.quiet++; s.held.quiet < heldTicks {
			return nil
		}
		return s.dropHeld()
	}
	page, err := capture(s.path)
	if err != nil {
		return err
	}
	if page.stamp() != s.held.Placed {
		return s.dropHeld()
	}
	if err := swapFiles(s.heldPath(), s.path); err != nil {
		return err
	}
	back, err := capture(s.heldPath())
	if err != nil {
		return err
	}
	if !back.stamp().sameContent(s.held.Placed) {
		if err := swapFiles(s.heldPath(), s.path); err != nil {
			return err
		}
		if back, err = capture(s.heldPath()); err != nil {
			return err
		}
	}
	if s.held, err = holding(s.heldPath(), s.path, back); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	return s.saveState(state{Rev: s.rev, Undo: s.undo})
}

func (s *Store) dropHeld() error {
	if s.held == nil {
		return nil
	}
	if err := os.Remove(s.heldPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.held = nil
	return s.saveState(state{Rev: s.rev, Undo: s.undo})
}

var settle = func() { time.Sleep(2 * time.Millisecond) }

func settled(name string, p placement) (bool, error) {
	settle()
	return p.holds(name)
}

type placement struct {
	fi os.FileInfo
	b  []byte
}

func capture(name string) (placement, error) {
	fi, err := os.Stat(name)
	if err != nil {
		return placement{}, err
	}
	b, err := os.ReadFile(name)
	return placement{fi, b}, err
}

func (p placement) holds(name string) (bool, error) {
	now, err := capture(name)
	if err != nil {
		return false, err
	}
	if p.fi != nil && (!os.SameFile(now.fi, p.fi) || now.fi.Size() != p.fi.Size() || !now.fi.ModTime().Equal(p.fi.ModTime())) {
		return false, nil
	}
	return bytes.Equal(now.b, p.b), nil
}

func tiedTmpIsNewer(tmp, path string, prev, placed placement) (bool, error) {
	kept, err := placed.holds(path)
	if err != nil || !kept {
		return false, err
	}
	stale, err := prev.holds(tmp)
	return !stale, err
}

func stamp(name string, mine os.FileInfo, ours []byte) (time.Time, error) {
	fi, err := os.Stat(name)
	if err != nil || !os.SameFile(fi, mine) {
		return fi.ModTime(), err
	}
	b, err := os.ReadFile(name)
	if err != nil || bytes.Equal(b, ours) {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

func apply(src []byte, op *Op) ([]byte, Summary, error) {
	els := parse(src)
	e, err := find(els, op.ID)
	if err != nil {
		return nil, Summary{}, err
	}
	one := []string{op.ID}
	about := func(verb string) string { return verb + " " + describe(src, e) }
	switch op.Kind {
	case "edit":
		with := op.HTML
		if op.Inner != nil {
			if op.HTML != "" {
				return nil, Summary{}, errors.New("an edit takes html or inner, not both")
			}
			with = string(src[e.start:e.tagEnd]) + *op.Inner + string(src[e.closeStart:e.end])
		}
		out, err := edit(src, els, e, with, op.Inner != nil)
		if err != nil {
			return nil, Summary{}, err
		}
		return out, Summary{Text: "edited " + describeID(out, op.ID), IDs: one}, nil
	case "cells":
		with, err := setCells(src, els, e, op.Cells)
		if err != nil {
			return nil, Summary{}, err
		}
		out, err := edit(src, els, e, with, false)
		if err != nil {
			return nil, Summary{}, err
		}
		return out, Summary{Text: "edited cells of " + describeID(out, op.ID), IDs: one}, nil
	case "delete":
		for i := range els {
			l := &els[i]
			href, _ := l.attr("href")
			if len(href) < 2 || href[0] != '#' || e.contains(l) {
				continue
			}
			if t, err := find(els, href[1:]); err == nil && e.contains(t) {
				return nil, Summary{}, fmt.Errorf("%w: a link to #%s", ErrInboundLinks, t.id)
			}
		}
		return cut(src, e), Summary{Text: about("deleted"), IDs: one}, nil
	case "archive":
		return setAttr(src, e, stateAttr, "archived"), Summary{Text: about("archived"), IDs: one}, nil
	case "unarchive":
		return removeAttr(src, e, stateAttr), Summary{Text: about("unarchived"), IDs: one}, nil
	case "check":
		if e.name != "kapi-task" {
			return nil, Summary{}, fmt.Errorf("check needs a kapi-task, and #%s is a <%s>", op.ID, e.name)
		}
		if op.Done {
			return setFlag(src, e, "done"), Summary{Text: "checked", IDs: one}, nil
		}
		return removeAttr(src, e, "done"), Summary{Text: "unchecked", IDs: one}, nil
	case "orient":
		if t, _ := e.attr("type"); e.name != "kapi-diagram" || (t != "" && t != "flow" && t != "architecture") {
			return nil, Summary{}, fmt.Errorf("orient needs a flow or architecture kapi-diagram, and #%s is not one", op.ID)
		}
		texts := map[string]string{"tb": "drew top to bottom", "lr": "drew left to right"}
		text, ok := texts[op.Dir]
		if !ok {
			return nil, Summary{}, fmt.Errorf("orient needs dir tb or lr, not %q", op.Dir)
		}
		return setAttr(src, e, "dir", op.Dir), Summary{Text: text, IDs: one}, nil
	case "move":
		out, err := move(src, els, e, op.Before)
		text := about("moved #"+op.ID) + " to the end"
		if op.Before != "" {
			text = about("moved #"+op.ID) + " before #" + op.Before
		}
		return out, Summary{Text: text}, err
	}
	return nil, Summary{}, fmt.Errorf("unknown op kind %q", op.Kind)
}

func edit(src []byte, els []element, e *element, with string, inner bool) ([]byte, error) {
	frag, root, err := fragment(with)
	if err != nil && inner {
		return nil, ErrInnerEscapes
	}
	if err != nil {
		return nil, err
	}
	if inner && root.closeStart != len(frag)-(e.end-e.closeStart) {
		return nil, ErrInnerEscapes
	}
	if root.closeStart < root.end {
		fels := parse(frag)
		for i := 1; i < len(fels); i++ {
			if f := &fels[i]; !voidElements[f.name] && !f.selfClosing && f.closeStart == f.end && !closedByHTML(fels, i) {
				return nil, ErrInnerEscapes
			}
		}
	}
	if !strings.HasPrefix(root.name, "kapi-") {
		for _, f := range parse(frag) {
			if _, ok := f.attr("data-kapi-preset"); ok || (strings.HasPrefix(f.name, "kapi-") && !render.Inline(f.name)) {
				return nil, ErrPresetInProse
			}
		}
	}
	if root.id != "" && root.id != e.id {
		return nil, fmt.Errorf("edit cannot change the id #%s to #%s", e.id, root.id)
	}
	outside := map[string]bool{}
	for i := range els {
		if !e.contains(&els[i]) && els[i].id != "" {
			outside[els[i].id] = true
		}
	}
	for _, f := range parse(frag) {
		if f.start != 0 && outside[f.id] {
			return nil, fmt.Errorf("edit html reuses #%s, which is elsewhere on the page", f.id)
		}
	}
	if root.id == "" {
		frag = setAttr(frag, root, idAttr, e.id)
	}
	out := splice(src, e.start, e.end, string(frag))
	if !sameTopLevel(src, out, e.id) {
		return nil, ErrInnerEscapes
	}
	before := dangling(src)
	var added []string
	for id := range dangling(out) {
		if !before[id] {
			added = append(added, "#"+id)
		}
	}
	if len(added) > 0 {
		slices.Sort(added)
		return nil, fmt.Errorf("%w: %s", ErrDanglingLinks, strings.Join(added, ", "))
	}
	return out, nil
}

func dangling(src []byte) map[string]bool {
	els := parse(src)
	ids := map[string]bool{}
	for _, e := range els {
		ids[e.id] = true
	}
	out := map[string]bool{}
	for i := range els {
		if href, _ := els[i].attr("href"); len(href) > 1 && href[0] == '#' && !ids[href[1:]] {
			out[href[1:]] = true
		}
	}
	return out
}

func move(src []byte, els []element, e *element, before string) ([]byte, error) {
	text := string(src[e.start:e.end])
	ls, ownLine := lineStart(src, e.start)
	indent := ""
	if ownLine {
		indent = string(src[ls:e.start])
	}
	var at int
	var ins string
	switch {
	case before != "":
		b, err := find(els, before)
		if err != nil {
			return nil, err
		}
		if e.contains(b) {
			return nil, fmt.Errorf("cannot move #%s before #%s, which is inside it", e.id, before)
		}
		at, ins = b.start, text
		if bls, ok := lineStart(src, b.start); ok && ownLine {
			ins = text + "\n" + string(src[bls:b.start])
		}
	case e.parent < 0:
		at, ins = len(src), indent+text+"\n"
		if len(src) > 0 && src[len(src)-1] != '\n' {
			ins = "\n" + ins
		}
	default:
		pc := els[e.parent].closeStart
		at, ins = pc, text
		if pls, ok := lineStart(src, pc); ok && ownLine {
			at, ins = pls, indent+text+"\n"
		}
	}
	rs, re := cutRange(src, e)
	if at >= re {
		return slices.Concat(src[:rs], src[re:at], []byte(ins), src[at:]), nil
	}
	return slices.Concat(src[:at], []byte(ins), src[at:rs], src[re:]), nil
}

func changedBlocks(old, cur []byte) []string {
	blocks := func(src []byte) ([]string, map[string]string) {
		var ids []string
		outer := map[string]string{}
		for _, e := range parse(src) {
			if e.parent < 0 && e.id != "" {
				ids = append(ids, e.id)
				outer[e.id] = string(src[e.start:e.end])
			}
		}
		return ids, outer
	}
	old, errOld := render.Canonical(old)
	cur, errCur := render.Canonical(cur)
	if errOld != nil || errCur != nil {
		return nil
	}
	oldIDs, oldOuter := blocks(old)
	curIDs, curOuter := blocks(cur)
	if !slices.Equal(oldIDs, curIDs) {
		return nil
	}
	var changed []string
	for _, id := range curIDs {
		if oldOuter[id] != curOuter[id] {
			changed = append(changed, id)
		}
	}
	return changed
}

const (
	excerptLen   = 60
	listedIDs    = 3
	presetPrefix = "kapi-"
)

var inline = map[string]bool{
	"a": true, "abbr": true, "b": true, "cite": true, "code": true, "em": true, "i": true, "kbd": true,
	"mark": true, "q": true, "s": true, "small": true, "span": true, "strong": true, "sub": true, "sup": true, "u": true,
}

func describeIDs(src []byte, ids []string) string {
	parts := make([]string, 0, listedIDs+1)
	for _, id := range ids[:min(len(ids), listedIDs)] {
		parts = append(parts, describeID(src, id))
	}
	if extra := len(ids) - listedIDs; extra > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", extra))
	}
	return strings.Join(parts, ", ")
}

func describeID(src []byte, id string) string {
	e, err := find(parse(src), id)
	if err != nil {
		return "#" + id
	}
	return describe(src, e)
}

func describe(src []byte, e *element) string {
	var parts []string
	if name, ok := strings.CutPrefix(e.name, presetPrefix); ok {
		parts = append(parts, name)
	}
	if text := excerpt(src[e.start:e.end]); text != "" || len(parts) == 0 {
		parts = append(parts, `"`+text+`"`)
	}
	return strings.Join(parts, " ")
}

func excerpt(outer []byte) string {
	var b strings.Builder
	z := html.NewTokenizer(bytes.NewReader(render.MaskRawBodies(outer)))
	off := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		start := off
		off += len(z.Raw())
		switch tt {
		case html.TextToken:
			b.WriteString(html.UnescapeString(string(outer[start:off])))
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			if name, _ := z.TagName(); !inline[string(name)] {
				b.WriteByte(' ')
			}
		}
	}
	text := []rune(strings.Join(strings.Fields(eventlog.Printable(b.String())), " "))
	if len(text) <= excerptLen {
		return string(text)
	}
	cut := string(text[:excerptLen])
	if text[excerptLen] != ' ' {
		if i := strings.LastIndexByte(cut, ' '); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimRight(cut, " ") + "\u2026"
}

func setCells(src []byte, els []element, e *element, cells []Cell) (string, error) {
	if e.name != "kapi-table" {
		return "", fmt.Errorf("a cells op needs a kapi-table, not <%s>", e.name)
	}
	if len(cells) == 0 {
		return "", errors.New("a cells op needs at least one cell")
	}
	table := -1
	var rows [][]*element
	for i := range els {
		c := &els[i]
		switch {
		case !e.contains(c):
		case c.name == "table" && table < 0:
			table = i
		case c.name == "tr" && table >= 0 && nearest(els, i, "table") == table:
			rows = append(rows, nil)
		case (c.name == "td" || c.name == "th") && len(rows) > 0 && c.parent >= 0 && els[c.parent].name == "tr" && nearest(els, c.parent, "table") == table:
			rows[len(rows)-1] = append(rows[len(rows)-1], c)
		}
	}
	var changes []cellChange
	seen := map[[2]int]bool{}
	for _, c := range cells {
		if c.Row < 0 || c.Row >= len(rows) || c.Col < 0 || c.Col >= len(rows[c.Row]) {
			return "", fmt.Errorf("the table has no cell at row %d, column %d", c.Row, c.Col)
		}
		if seen[[2]int{c.Row, c.Col}] {
			return "", fmt.Errorf("the cell at row %d, column %d is set twice", c.Row, c.Col)
		}
		seen[[2]int{c.Row, c.Col}] = true
		cell := rows[c.Row][c.Col]
		changes = append(changes, cellChange{cell.tagEnd, cell.closeStart, html.EscapeString(c.Text)})
	}
	outer, err := spliceCells(src[e.start:e.end], e.start, changes)
	return string(outer), err
}

type cellChange struct {
	from, to int
	text     string
}

func spliceCells(outer []byte, base int, changes []cellChange) ([]byte, error) {
	slices.SortFunc(changes, func(a, b cellChange) int { return b.from - a.from })
	for i := 1; i < len(changes); i++ {
		if changes[i].to > changes[i-1].from {
			return nil, errors.New("two cells of the op overlap in the source")
		}
	}
	for _, c := range changes {
		outer = splice(outer, c.from-base, c.to-base, c.text)
	}
	return outer, nil
}

func nearest(els []element, i int, name string) int {
	for p := els[i].parent; p >= 0; p = els[p].parent {
		if els[p].name == name {
			return p
		}
	}
	return -1
}

func sameTopLevel(src, out []byte, id string) bool {
	was, wasN := topLevel(src)
	is, isN := topLevel(out)
	if slices.Equal(was, is) && isN >= wasN {
		return true
	}
	lost := slices.DeleteFunc(slices.Clone(was), func(v string) bool { return v == id })
	return slices.Equal(lost, is) && isN >= wasN-1
}

func topLevel(src []byte) ([]string, int) {
	canon, err := render.Canonical(src)
	if err != nil {
		return nil, 0
	}
	var ids []string
	n := 0
	for _, e := range parse(canon) {
		if e.parent < 0 {
			n++
			if e.id != "" {
				ids = append(ids, e.id)
			}
		}
	}
	return ids, n
}

var optionalEnd = map[string]bool{
	"li": true, "dt": true, "dd": true, "p": true, "td": true, "th": true, "tr": true,
	"option": true, "thead": true, "tbody": true, "tfoot": true,
}

func closedByHTML(els []element, i int) bool {
	f := &els[i]
	if !optionalEnd[f.name] {
		return false
	}
	a := f.parent
	for a >= 0 && els[a].closeStart == els[a].end {
		a = els[a].parent
	}
	return a < 0 || f.end < els[a].closeStart || !strings.HasPrefix(els[a].name, "kapi-")
}
