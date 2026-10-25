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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/xaaha/kapi/internal/attach"
	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/render"
)

var (
	ErrStale         = errors.New("page changed since that revision")
	ErrNotFound      = errors.New("no such element")
	ErrInboundLinks  = errors.New("other elements link to it")
	ErrNothingToUndo = errors.New("nothing to undo")
	ErrNoImage       = errors.New("that image is not in the page's files folder")
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
	Parts  []Part  `json:"parts,omitempty"`
	Before string  `json:"before,omitempty"`
	Done   bool    `json:"done,omitempty"`
	Dir    string  `json:"dir,omitempty"`
	Src    string  `json:"src,omitempty"`
}

type Summary struct {
	Text string
	IDs  []string
	Seq  uint64
}

type Store struct {
	mu   sync.Mutex
	path string
	dir  string
	src  []byte
	rev  uint64
	undo []uint64

	conversion string
	held       []heldFile

	log    *eventlog.Log
	event  *revEvent
	logged uint64
}

type state struct {
	Rev   uint64     `json:"rev"`
	Undo  []uint64   `json:"undo"`
	Next  *state     `json:"next,omitempty"`
	Temp  string     `json:"temp,omitempty"`
	Held  []heldFile `json:"held,omitempty"`
	Event *revEvent  `json:"event,omitempty"`
}

type revEvent struct {
	Rev     uint64   `json:"rev"`
	Author  string   `json:"author"`
	Text    string   `json:"text"`
	IDs     []string `json:"ids,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

type heldFile struct {
	Name   string    `json:"name"`
	Seen   fileStamp `json:"seen"`
	Placed fileStamp `json:"placed"`
	Until  int64     `json:"until"`
	Back   *swapBack `json:"back,omitempty"`
}

type swapBack struct {
	Into fileStamp `json:"into"`
	Out  fileStamp `json:"out"`
}

type fileStamp struct {
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
	Sum   string `json:"sum"`
}

const heldFor = 10 * time.Second

var clock = time.Now

// ID is the 16-hex directory name under $KAPI_HOME/pages for an absolute page path.
func ID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:8])
}

func Open(home, path string, l *eventlog.Log) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("page path must be absolute: %q", path)
	}
	s := &Store{path: path, dir: filepath.Join(home, "pages", ID(path)), log: l}
	events, _ := l.After(0)
	for i := range events {
		if e := &events[i]; e.Kind == "revision" {
			s.logged = max(s.logged, e.Rev)
		}
	}
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
	if err := s.restoreDisplaced(&st); err != nil {
		return nil, err
	}
	if st.Next != nil {
		if err := s.saveState(&state{Rev: s.rev, Undo: s.undo}); err != nil {
			return nil, err
		}
	}
	if err := s.recoverSwapBacks(); err != nil {
		return nil, err
	}
	if _, err := s.checkHeld(true); err != nil {
		return nil, err
	}
	if _, err := s.logEvent(); err != nil {
		return nil, err
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
		var ev *revEvent
		if s.rev > 0 && !bytes.Equal(raw, s.src) {
			ev = fileEvent(s.src, src, text)
		} else if text != "" {
			ev = &revEvent{Author: "kapi", Text: text}
		}
		_, err = s.commit(src, raw, s.rev+1, s.undo, ev)
		if errors.Is(err, errFileChanged) && attempt < 10 {
			continue
		}
		if errors.Is(err, errFileChanged) {
			var last *revEvent
			if s.rev > 0 && !bytes.Equal(raw, s.src) {
				last = fileEvent(s.src, raw, "")
			}
			_, err = s.commit(raw, raw, s.rev+1, s.undo, last)
		}
		if err != nil {
			return nil, err
		}
		if bytes.Equal(s.src, src) {
			s.conversion = text
		}
		return s, nil
	}
}

func (s *Store) load(st *state) error {
	s.rev, s.undo, s.event = st.Rev, st.Undo, st.Event
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
		s.src, s.rev, s.undo, s.event = after, st.Next.Rev, st.Next.Undo, st.Next.Event
	}
	return nil
}

func (s *Store) restoreDisplaced(st *state) error {
	temps, err := findTemps(filepath.Dir(s.path), filepath.Base(s.path))
	if err != nil {
		return err
	}
	orphans, err := findTemps(s.heldDir(), filepath.Base(s.path))
	if err != nil {
		return err
	}
	for _, o := range orphans {
		if slices.ContainsFunc(st.Held, func(h heldFile) bool { return h.Name == filepath.Base(o) }) {
			continue
		}
		if slices.ContainsFunc(temps, func(t string) bool { return filepath.Base(t) == filepath.Base(o) }) {
			if err := os.Remove(o); err != nil {
				return err
			}
			continue
		}
		temps = append(temps, o)
	}
	if len(temps) == 0 {
		return nil
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
			if err == nil {
				page = theirs
			}
			if errors.Is(err, errFileChanged) {
				err = s.keep(tmp)
			}
			if err != nil {
				return err
			}
			if err := syncDir(filepath.Dir(s.path)); err != nil {
				return err
			}
		case named && bytes.Equal(page, before):
		default:
			if err := s.keep(tmp); err != nil {
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

func (s *Store) keep(tmp string) error {
	dir, name := filepath.Dir(s.path), filepath.Base(tmp)
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
	if err := WriteAtomic(s.rawPath(rev), raw); err != nil {
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

var ErrNoRev = errors.New("no such revision")

func (s *Store) SourceAt(rev uint64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case rev == s.rev:
		return slices.Clone(s.src), nil
	case rev == 0 || rev > s.rev:
		return nil, ErrNoRev
	}
	src, err := s.readRev(rev)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoRev
	}
	return src, err
}

func (s *Store) CanUndo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.undo)
	if n == 0 {
		return false
	}
	after, err := os.ReadFile(s.revPath(s.undo[n-1] + 1))
	return err == nil && bytes.Equal(after, s.src)
}

// Refresh picks up an edit made to the file since kapi last read or wrote it.
func (s *Store) Refresh() (Summary, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.logEvent(); err != nil {
		return Summary{}, false, err
	}
	if _, err := s.checkHeld(false); err != nil {
		return Summary{}, false, err
	}
	for attempt := 0; attempt < 10; attempt++ {
		raw, err := os.ReadFile(s.path)
		if err != nil || bytes.Equal(raw, s.src) {
			return Summary{}, false, err
		}
		src, text, err := s.absorb(raw, s.rev+1)
		if err != nil {
			return Summary{}, false, err
		}
		ev := fileEvent(s.src, src, text)
		seq, err := s.commit(src, raw, s.rev+1, s.undo, ev)
		if errors.Is(err, errFileChanged) {
			continue
		}
		if err != nil {
			return Summary{}, false, err
		}
		return Summary{Text: ev.Text, IDs: ev.IDs, Seq: seq}, true, nil
	}
	return Summary{}, false, nil
}

func (s *Store) Apply(rev uint64, op *Op) (uint64, Summary, error) {
	return s.ApplyBy("human", rev, op)
}

func (s *Store) ApplyBy(author string, rev uint64, op *Op) (uint64, Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.logEvent(); err != nil {
		return s.rev, Summary{}, err
	}
	if _, err := s.checkHeld(false); err != nil {
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
		sum := Summary{Text: "undid back to revision " + strconv.FormatUint(back, 10)}
		if sum.Seq, err = s.commit(src, raw, s.rev+1, s.undo[:n-1], &revEvent{Author: "human", Text: sum.Text}); err != nil {
			return s.rev, Summary{}, stale(err)
		}
		return s.rev, sum, nil
	}
	if op.Kind == "image" {
		if !attach.ValidName(op.Src) {
			return s.rev, Summary{}, fmt.Errorf("%w: %q", ErrNoImage, op.Src)
		}
		if _, err := os.Stat(filepath.Join(FilesDir(s.path), op.Src)); err != nil {
			return s.rev, Summary{}, fmt.Errorf("%w: %s", ErrNoImage, op.Src)
		}
		src := "./" + url.PathEscape(filesName(s.path)) + "/" + op.Src
		op = &Op{Kind: op.Kind, ID: op.ID, HTML: `<img src="` + html.EscapeString(src) + `" alt="">`}
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
	undo := s.undo
	if author == "human" {
		undo = append(undo, s.rev)
	}
	if sum.Seq, err = s.commit(src, raw, s.rev+1, undo, &revEvent{Author: author, Text: sum.Text, IDs: sum.IDs}); err != nil {
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
	case "image":
		if op.ID == "" {
			return true
		}
	case "edit", "text", "archive", "unarchive", "delete", "check", "orient":
	default:
		return false
	}
	then, err := os.ReadFile(s.revPath(rev))
	if err != nil {
		return false
	}
	if op.Kind == "text" {
		return textUnchanged(then, s.src, op)
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
	appendEvent    = (*eventlog.Log).Append
	syncFile       = (*os.File).Sync
	renameFile     = os.Rename
	swapFiles      = exchangeFiles
	writeTemp      = (*os.File).Write
)

func (s *Store) commit(src, base []byte, rev uint64, undo []uint64, ev *revEvent) (uint64, error) {
	swapped, err := s.checkHeld(false)
	if err != nil {
		return 0, err
	}
	if swapped {
		return 0, errFileChanged
	}
	if !bytes.Equal(src, base) {
		quiet, err := settled(s.path, placement{b: base})
		if err != nil {
			return 0, err
		}
		if !quiet {
			return 0, errFileChanged
		}
	}
	if err := WriteAtomic(s.revPath(rev), src); err != nil {
		return 0, err
	}
	if ev != nil {
		ev.Rev = rev
	}
	next := state{Rev: rev, Undo: undo}
	if !bytes.Equal(src, base) {
		now := state{Rev: s.rev, Undo: s.undo}
		var n [4]byte
		if _, err := rand.Read(n[:]); err != nil {
			return 0, err
		}
		intent := state{Rev: rev, Undo: undo, Event: ev, Temp: "." + filepath.Base(s.path) + tempMark + strconv.FormatUint(uint64(binary.BigEndian.Uint32(n[:])), 10)}
		if err := s.saveState(&state{Rev: s.rev, Undo: s.undo, Next: &intent}); err != nil {
			return 0, err
		}
		tmp := filepath.Join(filepath.Dir(s.path), intent.Temp)
		m, err := replaceFileVia(tmp, s.path, src, base)
		if m != nil {
			err = errors.Join(err, s.hold(tmp, m))
		}
		if err != nil {
			return 0, errors.Join(err, s.saveState(&now))
		}
	}
	s.src, s.rev, s.undo, s.event = src, rev, undo, ev
	if err := s.saveState(&next); err != nil {
		return 0, err
	}
	return s.logEvent()
}

func (s *Store) logEvent() (uint64, error) {
	if s.event == nil || s.event.Rev <= s.logged {
		return 0, nil
	}
	e, err := appendEvent(s.log, &eventlog.Event{Author: s.event.Author, Kind: "revision", Rev: s.event.Rev, Target: strings.Join(s.event.IDs, ","), Changed: strings.Join(s.event.Changed, ","), Text: s.event.Text})
	if err != nil {
		return 0, err
	}
	s.logged = s.event.Rev
	return e.Seq, nil
}

func (s *Store) saveState(st *state) error {
	st.Held, st.Event = s.held, s.event
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return WriteAtomic(filepath.Join(s.dir, "state.json"), b)
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

func WriteAtomic(path string, b []byte) error {
	return replaceFile(path, b, nil)
}

func replaceFile(path string, b, expect []byte) error {
	_, err := replaceFileVia("", path, b, expect)
	return err
}

func replaceFileVia(tmp, path string, b, expect []byte) (*moved, error) {
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
	var h *moved
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

func swapIn(tmp, path string, ours, expect []byte) (*moved, error) {
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
		return holding(path, displaced)
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
			h, err := holding(path, seen)
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

type moved struct{ seen, placed placement }

func holding(path string, seen placement) (*moved, error) {
	at, err := capture(path)
	if err != nil {
		return nil, err
	}
	return &moved{seen, at}, nil
}

func (p placement) stamp() fileStamp {
	sum := sha256.Sum256(p.b)
	return fileStamp{Size: p.fi.Size(), Mtime: p.fi.ModTime().UnixNano(), Sum: hex.EncodeToString(sum[:])}
}

func (s *Store) heldDir() string { return filepath.Join(s.dir, "held") }

func (s *Store) hold(tmp string, m *moved) error {
	defer os.Remove(tmp)
	if err := os.MkdirAll(s.heldDir(), 0o700); err != nil {
		return err
	}
	if err := os.Link(tmp, filepath.Join(s.heldDir(), filepath.Base(tmp))); err != nil {
		return nil
	}
	s.placed(m.placed.stamp())
	s.held = append(s.held, heldFile{Name: filepath.Base(tmp), Seen: m.seen.stamp(), Placed: m.placed.stamp(), Until: clock().Add(heldFor).UnixNano()})
	return nil
}

func (s *Store) placed(page fileStamp) {
	for i := range s.held {
		s.held[i].Placed = page
	}
}

// Tick swaps back any late write into a file kapi moved aside, and retires held files that stayed unchanged for heldFor.
func (s *Store) Tick() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.checkHeld(false)
	return err
}

// Settle swaps back late writes and deletes every held file, for a daemon that is stopping.
func (s *Store) Settle() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.checkHeld(true)
	return err
}

func (s *Store) checkHeld(final bool) (bool, error) {
	if len(s.held) == 0 {
		return false, nil
	}
	swapped, changed := false, false
	for i := 0; i < len(s.held); {
		keep, back, err := s.settleHeld(i, final)
		if err != nil {
			return swapped, err
		}
		swapped = swapped || back
		if keep && !back {
			i++
			continue
		}
		changed = true
		if !keep {
			s.held = slices.Delete(s.held, i, i+1)
		} else {
			i++
		}
	}
	if !changed {
		return false, nil
	}
	return swapped, s.saveState(&state{Rev: s.rev, Undo: s.undo})
}

func (s *Store) settleHeld(i int, final bool) (keep, back bool, err error) {
	e := &s.held[i]
	link := filepath.Join(s.heldDir(), e.Name)
	held, err := capture(link)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	page, err := capture(s.path)
	if err != nil {
		return false, false, err
	}
	switch {
	case held.stamp() == e.Seen:
		if !final && clock().UnixNano() < e.Until {
			return true, false, nil
		}
		return false, false, os.Remove(link)
	case page.stamp() != e.Placed:
		if bytes.Equal(held.b, page.b) {
			return false, false, os.Remove(link)
		}
		return false, false, s.keep(link)
	}
	e.Back = &swapBack{Into: held.stamp(), Out: page.stamp()}
	if err := s.saveState(&state{Rev: s.rev, Undo: s.undo}); err != nil {
		return false, false, err
	}
	if err := swapFiles(link, s.path); err != nil {
		return false, false, err
	}
	out, err := capture(link)
	if err != nil {
		return false, true, err
	}
	if out.stamp() != e.Back.Out {
		if err := swapFiles(link, s.path); err != nil {
			return false, true, err
		}
		return false, true, s.keep(link)
	}
	if err := syncDir(filepath.Dir(s.path)); err != nil {
		return false, true, err
	}
	into := e.Back.Into
	s.placed(into)
	*e = heldFile{Name: e.Name, Seen: out.stamp(), Placed: into, Until: clock().Add(heldFor).UnixNano()}
	if final {
		return false, true, os.Remove(link)
	}
	return true, true, nil
}

func (s *Store) recoverSwapBacks() error {
	for i := 0; i < len(s.held); {
		e := &s.held[i]
		if e.Back == nil {
			i++
			continue
		}
		link := filepath.Join(s.heldDir(), e.Name)
		held, err := capture(link)
		if errors.Is(err, os.ErrNotExist) {
			s.held = slices.Delete(s.held, i, i+1)
			continue
		}
		if err != nil {
			return err
		}
		page, err := capture(s.path)
		if err != nil {
			return err
		}
		switch {
		case page.stamp() == e.Back.Into && held.stamp() == e.Back.Out:
			*e = heldFile{Name: e.Name, Seen: e.Back.Out, Placed: e.Back.Into, Until: e.Until}
		case page.stamp() == e.Back.Out:
			e.Back = nil
		default:
			if !bytes.Equal(held.b, page.b) {
				if err := s.keep(link); err != nil {
					return err
				}
			} else if err := os.Remove(link); err != nil {
				return err
			}
			s.held = slices.Delete(s.held, i, i+1)
			continue
		}
		i++
	}
	return nil
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

func FilesDir(path string) string {
	return filepath.Join(filepath.Dir(path), filesName(path))
}

func filesName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base)) + ".files"
}

func insertImage(src []byte, els []element, op *Op) ([]byte, Summary, error) {
	taken := map[string]bool{}
	body := -1
	for i := range els {
		if els[i].id != "" {
			taken[els[i].id] = true
		}
		if els[i].name == "body" && body < 0 {
			body = i
		}
	}
	id := newID(taken)
	fig := `<figure data-kapi-id="` + id + `">` + op.HTML + `</figure>`
	sum := Summary{IDs: []string{id}}
	if op.ID == "" {
		at := len(src)
		if body >= 0 {
			at = els[body].closeStart
		}
		ins := fig + "\n"
		if at > 0 && src[at-1] != '\n' {
			ins = "\n" + ins
		}
		sum.Text = "added an image at the end"
		return slices.Concat(src[:at], []byte(ins), src[at:]), sum, nil
	}
	e, err := find(els, op.ID)
	if err != nil {
		return nil, Summary{}, err
	}
	for e.parent >= 0 && !transparent(els[e.parent].name) {
		e = &els[e.parent]
	}
	ins := fig
	if ls, ownLine := lineStart(src, e.start); ownLine {
		ins = "\n" + string(src[ls:e.start]) + fig
	}
	sum.Text = "added an image after " + describe(src, e)
	return slices.Concat(src[:e.end], []byte(ins), src[e.end:]), sum, nil
}

func apply(src []byte, op *Op) ([]byte, Summary, error) {
	els := parse(src)
	if op.Kind == "image" {
		return insertImage(src, els, op)
	}
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
		out, err := edit(src, els, e, with, op.Inner != nil, false)
		if err != nil {
			return nil, Summary{}, err
		}
		return out, Summary{Text: "edited " + describeID(out, op.ID), IDs: one}, nil
	case "text":
		out, err := setText(src, op)
		if err != nil {
			return nil, Summary{}, err
		}
		return out, Summary{Text: "edited " + describeID(out, op.ID), IDs: one}, nil
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

func edit(src []byte, els []element, e *element, with string, inner, keptPresets bool) ([]byte, error) {
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
	if !strings.HasPrefix(root.name, "kapi-") && !keptPresets {
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

func fileEvent(old, cur []byte, conversion string) *revEvent {
	ids, changed := changedBlocks(old, cur)
	summary := "edited in the file"
	if len(ids) > 0 {
		summary += ": " + describeIDs(cur, ids)
	}
	if conversion != "" {
		summary += "; " + conversion
	}
	return &revEvent{Author: "file", Text: summary, IDs: ids, Changed: changed}
}

func changedBlocks(old, cur []byte) (ids, changed []string) {
	blocks := func(src []byte) (top, all []string, outer map[string]string) {
		outer = map[string]string{}
		for _, e := range parse(src) {
			if e.id == "" {
				continue
			}
			all = append(all, e.id)
			outer[e.id] = string(src[e.start:e.end])
			if e.parent < 0 {
				top = append(top, e.id)
			}
		}
		return top, all, outer
	}
	old, errOld := render.Canonical(old)
	cur, errCur := render.Canonical(cur)
	if errOld != nil || errCur != nil {
		return nil, nil
	}
	oldTop, oldAll, oldOuter := blocks(old)
	curTop, _, curOuter := blocks(cur)
	for _, id := range oldAll {
		if now, ok := curOuter[id]; !ok || now != oldOuter[id] {
			changed = append(changed, id)
		}
	}
	if !slices.Equal(oldTop, curTop) {
		return nil, changed
	}
	for _, id := range curTop {
		if oldOuter[id] != curOuter[id] {
			ids = append(ids, id)
		}
	}
	return ids, changed
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
