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
	"sync"
)

var (
	ErrStale         = errors.New("page changed since that revision")
	ErrNotFound      = errors.New("no such element")
	ErrInboundLinks  = errors.New("other elements link to it")
	ErrNothingToUndo = errors.New("nothing to undo")
	ErrUndoConflict  = errors.New("the file changed outside the browser after that op")
)

type Op struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	HTML   string `json:"html,omitempty"`
	Before string `json:"before,omitempty"`
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := assignIDs(raw)
	if s.rev == 0 {
		err = s.commit(src, !bytes.Equal(raw, src), 1, nil)
	} else if s.src, err = os.ReadFile(s.revPath(s.rev)); err == nil {
		if bytes.Equal(raw, s.src) {
			return s, nil
		}
		err = s.commit(src, !bytes.Equal(raw, src), s.rev+1, s.undo)
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Source() ([]byte, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.src), s.rev
}

// Refresh picks up an edit made to the file since kapi last read or wrote it.
func (s *Store) Refresh() (Summary, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil || bytes.Equal(raw, s.src) {
		return Summary{}, false, err
	}
	old, src := s.src, assignIDs(raw)
	if err := s.commit(src, !bytes.Equal(raw, src), s.rev+1, s.undo); err != nil {
		return Summary{}, false, err
	}
	return Summary{Text: "edited in the file", IDs: changedBlocks(old, s.src)}, true, nil
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
		if err := s.commit(src, true, s.rev+1, s.undo[:n-1]); err != nil {
			return s.rev, Summary{}, err
		}
		return s.rev, Summary{Text: "undid back to revision " + strconv.FormatUint(back, 10)}, nil
	}
	src, sum, err := apply(s.src, op)
	if err != nil {
		return s.rev, Summary{}, err
	}
	if err := s.commit(src, true, s.rev+1, append(s.undo, s.rev)); err != nil {
		return s.rev, Summary{}, err
	}
	return s.rev, sum, nil
}

func (s *Store) blockUnchangedSince(rev uint64, op Op) bool {
	if op.Kind == "undo" {
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

func (s *Store) commit(src []byte, write bool, rev uint64, undo []uint64) error {
	if err := writeAtomic(s.revPath(rev), src); err != nil {
		return err
	}
	if write {
		if err := writeAtomic(s.path, src); err != nil {
			return err
		}
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
	return os.Rename(f.Name(), path)
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
	return assignIDs(splice(src, e.start, e.end, string(frag))), nil
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
