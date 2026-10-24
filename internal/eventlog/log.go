package eventlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Seq    uint64    `json:"seq"`
	Time   time.Time `json:"time"`
	Author string    `json:"author"`
	Kind   string    `json:"kind"`
	Target string    `json:"target,omitempty"`
	Rev    uint64    `json:"rev,omitempty"`
	Text   string    `json:"text"`
}

type Log struct {
	mu      sync.Mutex
	dir     string
	file    *os.File
	size    int64
	events  []Event
	pos     positions
	changed chan struct{}
	acked   chan struct{}
}

type positions struct {
	Seen   map[string]uint64   `json:"seen"`
	Queued map[string]uint64   `json:"queued"`
	Hooked map[string]uint64   `json:"hooked"`
	Cut    map[string][]uint64 `json:"cut,omitempty"`
}

const (
	eventsFile    = "events.ndjson"
	positionsFile = "positions.json"
	cursorsFile   = "cursors.json"
)

var (
	syncFile   = (*os.File).Sync
	renameFile = os.Rename
	writeLine  = (*os.File).Write
)

func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	events, whole, err := readEvents(filepath.Join(dir, eventsFile))
	if err != nil {
		return nil, err
	}
	pos, err := readPositions(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, eventsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(whole); err != nil {
		f.Close()
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		f.Close()
		return nil, err
	}
	return &Log{dir: dir, file: f, size: whole, events: events, pos: pos, changed: make(chan struct{}), acked: make(chan struct{})}, nil
}

// Summary reads a log's reader cursors and unread counts without opening it for writing.
func Summary(dir string) (map[string]uint64, func(reader string) int, error) {
	events, _, err := readEvents(filepath.Join(dir, eventsFile))
	if err != nil {
		return nil, nil, err
	}
	pos, err := readPositions(dir)
	if err != nil {
		return nil, nil, err
	}
	pending := func(reader string) int {
		c := pos.Seen[reader]
		if c >= uint64(len(events)) {
			return 0
		}
		return len(unread(events[c:], reader))
	}
	return pos.Seen, pending, nil
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

func (l *Log) Append(in *Event) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := *in
	e.Seq = uint64(len(l.events)) + 1
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return Event{}, err
	}
	line = append(line, '\n')
	if _, err := writeLine(l.file, line); err != nil {
		return Event{}, errors.Join(err, l.file.Truncate(l.size))
	}
	if err := syncFile(l.file); err != nil {
		return Event{}, errors.Join(err, l.file.Truncate(l.size))
	}
	l.size += int64(len(line))
	l.events = append(l.events, e)
	close(l.changed)
	l.changed = make(chan struct{})
	return e, nil
}

// After returns events with seq greater than cursor, and a channel closed on the next append.
func (l *Log) After(cursor uint64) ([]Event, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cursor >= uint64(len(l.events)) {
		return nil, l.changed
	}
	return slices.Clone(l.events[cursor:]), l.changed
}

func (l *Log) Cursor(reader string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pos.Seen[reader]
}

func (l *Log) Queued(reader string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return max(l.pos.Queued[reader], l.pos.Seen[reader])
}

func (l *Log) Hooked(reader string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return max(l.pos.Hooked[reader], l.pos.Seen[reader])
}

func (l *Log) Cut(reader string) []uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.pos.Cut[reader])
}

func (l *Log) Ack(reader string, seq uint64) error {
	return l.update(reader, seq, func(p *positions) { p.Seen[reader] = max(p.Seen[reader], seq) })
}

func (l *Log) Queue(reader string, seq uint64) error {
	return l.update(reader, seq, func(p *positions) { p.Queued[reader] = max(p.Queued[reader], seq) })
}

func (l *Log) Handover(reader string, seq uint64, cut []uint64) error {
	return l.update(reader, seq, func(p *positions) {
		p.Queued[reader] = max(p.Queued[reader], seq)
		p.Hooked[reader] = max(p.Hooked[reader], seq)
		cuts := slices.Concat(p.Cut[reader], slices.DeleteFunc(slices.Clone(cut), func(c uint64) bool { return c > seq }))
		slices.Sort(cuts)
		p.Cut[reader] = slices.Compact(cuts)
		through := seq
		if first := slices.IndexFunc(p.Cut[reader], func(c uint64) bool { return c > p.Seen[reader] }); first >= 0 {
			through = min(through, p.Cut[reader][first]-1)
		}
		p.Seen[reader] = max(p.Seen[reader], through)
	})
}

func (l *Log) update(reader string, seq uint64, change func(*positions)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq > uint64(len(l.events)) {
		return fmt.Errorf("position %d beyond last event %d", seq, len(l.events))
	}
	next := positions{Seen: maps.Clone(l.pos.Seen), Queued: maps.Clone(l.pos.Queued), Hooked: maps.Clone(l.pos.Hooked), Cut: maps.Clone(l.pos.Cut)}
	change(&next)
	next.Cut[reader] = slices.DeleteFunc(slices.Clone(next.Cut[reader]), func(c uint64) bool { return c <= next.Seen[reader] })
	if len(next.Cut[reader]) == 0 {
		delete(next.Cut, reader)
	}
	if err := writePositions(l.dir, &next); err != nil {
		return err
	}
	l.pos = next
	close(l.acked)
	l.acked = make(chan struct{})
	return nil
}

// Cursors returns every reader's seen and queued positions and a channel closed when the next one advances.
func (l *Log) Cursors() (seen, queued map[string]uint64, advanced <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.pos.Seen), maps.Clone(l.pos.Queued), l.acked
}

// Pending returns the events a reader has not acknowledged, leaving out presence events and the reader's own.
func (l *Log) Pending(reader string) []Event {
	events, _, _ := l.Unseen(reader)
	return events
}

func (l *Log) Unseen(reader string) (events []Event, through, queued uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	through = uint64(len(l.events))
	return unread(l.events[min(l.pos.Seen[reader], through):], reader), through, max(l.pos.Queued[reader], l.pos.Seen[reader])
}

const KindPresence = "presence"

func unread(events []Event, reader string) []Event {
	return slices.DeleteFunc(slices.Clone(events), func(e Event) bool { return e.Kind == KindPresence || e.Author == reader })
}

func readEvents(path string) ([]Event, int64, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	whole := bytes.LastIndexByte(b, '\n') + 1
	var events []Event
	for line := range bytes.Lines(b[:whole]) {
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, 0, fmt.Errorf("%s line %d: %w", path, len(events)+1, err)
		}
		events = append(events, e)
	}
	return events, int64(whole), nil
}

func readPositions(dir string) (positions, error) {
	p, err := readJSON(filepath.Join(dir, positionsFile), func(b []byte, p *positions) error { return json.Unmarshal(b, p) })
	if err != nil || p != nil {
		return filled(p), err
	}
	p, err = readJSON(filepath.Join(dir, cursorsFile), func(b []byte, p *positions) error { return json.Unmarshal(b, &p.Seen) })
	return filled(p), err
}

func readJSON(path string, decode func([]byte, *positions) error) (*positions, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p positions
	if decode(b, &p) != nil {
		return nil, nil
	}
	return &p, nil
}

func filled(p *positions) positions {
	if p == nil {
		p = &positions{}
	}
	for _, m := range []*map[string]uint64{&p.Seen, &p.Queued, &p.Hooked} {
		if *m == nil {
			*m = map[string]uint64{}
		}
	}
	if p.Cut == nil {
		p.Cut = map[string][]uint64{}
	}
	return *p
}

func writePositions(dir string, p *positions) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, positionsFile)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := renameFile(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return syncFile(d)
}

// Printable drops control and bidi characters a terminal would act on, turning newlines and tabs into spaces.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case r < 0x20, r >= 0x7f && r <= 0x9f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
			r == 0x200e, r == 0x200f, r == 0x061c, r == 0x2028, r == 0x2029:
			return -1
		}
		return r
	}, s)
}

// CheckReader refuses the author names the browser, file edits and kapi itself write events as.
func CheckReader(name string) error {
	if name == "human" || name == "kapi" || name == "file" {
		return fmt.Errorf("reader %q is reserved; pick another name, such as agent or codex", name)
	}
	return nil
}
