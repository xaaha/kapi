package page

import (
	"bytes"
	"crypto/sha256"
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
)

type Op struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	HTML   string `json:"html,omitempty"`
	Before string `json:"before,omitempty"`
	Done   bool   `json:"done,omitempty"`
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
}

type state struct {
	Rev  uint64   `json:"rev"`
	Undo []uint64 `json:"undo"`
}

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
	b, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var st state
		if err := json.Unmarshal(b, &st); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(s.dir, "state.json"), err)
		}
		s.rev, s.undo = st.Rev, st.Undo
	}
	if s.rev > 0 {
		if s.src, err = os.ReadFile(s.revPath(s.rev)); err != nil {
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
		if err != nil {
			return nil, err
		}
		s.conversion = text
		return s, nil
	}
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
		summary := "edited in the file"
		if text != "" {
			summary += "; " + text
		}
		err = s.commit(src, raw, s.rev+1, s.undo)
		if errors.Is(err, errFileChanged) {
			continue
		}
		if err != nil {
			return Summary{}, false, err
		}
		return Summary{Text: summary, IDs: changedBlocks(old, s.src)}, true, nil
	}
	return Summary{}, false, nil
}

func (s *Store) Apply(rev uint64, op Op) (uint64, Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *Store) blockUnchangedSince(rev uint64, op Op) bool {
	switch op.Kind {
	case "edit", "done", "archive", "delete", "check":
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
)

func (s *Store) commit(src, base []byte, rev uint64, undo []uint64) error {
	if !bytes.Equal(src, base) {
		if err := replaceFile(s.path, src, base); err != nil {
			return err
		}
	}
	if err := writeAtomic(s.revPath(rev), src); err != nil {
		return err
	}
	b, err := json.Marshal(state{Rev: rev, Undo: undo})
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.dir, "state.json"), b); err != nil {
		return err
	}
	s.src, s.rev, s.undo = src, rev, undo
	return nil
}

func writeAtomic(path string, b []byte) error {
	return replaceFile(path, b, nil)
}

func replaceFile(path string, b, expect []byte) error {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".kapi-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if expect == nil {
		return os.Rename(f.Name(), path)
	}
	return swapIn(f.Name(), path, b, expect)
}

func swapIn(tmp, path string, ours, expect []byte) error {
	mine, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if replaceHook != nil {
		replaceHook("swap")
	}
	err = swapFiles(tmp, path)
	if errors.Is(err, errNoSwap) {
		cur, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(cur, expect) {
			return errFileChanged
		}
		return os.Rename(tmp, path)
	}
	if err != nil {
		return err
	}
	if displaced, err := os.ReadFile(tmp); err != nil || bytes.Equal(displaced, expect) {
		return err
	}
	for range 1 << 16 {
		atPath, err := stamp(path, mine, ours)
		if err != nil {
			return err
		}
		atTmp, err := stamp(tmp, mine, ours)
		if err != nil {
			return err
		}
		if !atTmp.After(atPath) {
			return errFileChanged
		}
		if replaceHook != nil {
			replaceHook("restore")
		}
		if err := swapFiles(tmp, path); err != nil {
			return err
		}
	}
	return errFileChanged
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

func apply(src []byte, op Op) ([]byte, Summary, error) {
	els := parse(src)
	e, err := find(els, op.ID)
	if err != nil {
		return nil, Summary{}, err
	}
	one := []string{op.ID}
	switch op.Kind {
	case "edit":
		out, err := edit(src, els, e, op.HTML)
		return out, Summary{Text: "edited", IDs: one}, err
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
		return cut(src, e), Summary{Text: "deleted", IDs: one}, nil
	case "done":
		return setAttr(src, e, "data-kapi-state", "done"), Summary{Text: "marked done", IDs: one}, nil
	case "archive":
		return setAttr(src, e, "data-kapi-state", "archived"), Summary{Text: "archived", IDs: one}, nil
	case "check":
		if e.name != "kapi-task" {
			return nil, Summary{}, fmt.Errorf("check needs a kapi-task, and #%s is a <%s>", op.ID, e.name)
		}
		if op.Done {
			return setFlag(src, e, "done"), Summary{Text: "checked", IDs: one}, nil
		}
		return removeAttr(src, e, "done"), Summary{Text: "unchecked", IDs: one}, nil
	case "move":
		out, err := move(src, els, e, op.Before)
		text := "moved #" + op.ID + " to the end"
		if op.Before != "" {
			text = "moved #" + op.ID + " before #" + op.Before
		}
		return out, Summary{Text: text}, err
	}
	return nil, Summary{}, fmt.Errorf("unknown op kind %q", op.Kind)
}

func edit(src []byte, els []element, e *element, with string) ([]byte, error) {
	frag, root, err := fragment(with)
	if err != nil {
		return nil, err
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
