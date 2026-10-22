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
	Text   string    `json:"text"`
}

type Log struct {
	mu      sync.Mutex
	dir     string
	file    *os.File
	events  []Event
	cursors map[string]uint64
	changed chan struct{}
	acked   chan struct{}
}

const (
	eventsFile  = "events.ndjson"
	cursorsFile = "cursors.json"
)

var syncFile = (*os.File).Sync

func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	events, whole, err := readEvents(filepath.Join(dir, eventsFile))
	if err != nil {
		return nil, err
	}
	cursors, err := readCursors(filepath.Join(dir, cursorsFile))
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
	return &Log{dir: dir, file: f, events: events, cursors: cursors, changed: make(chan struct{}), acked: make(chan struct{})}, nil
}

// Summary reads a log's reader cursors and unread counts without opening it for writing.
func Summary(dir string) (map[string]uint64, func(reader string) int, error) {
	events, _, err := readEvents(filepath.Join(dir, eventsFile))
	if err != nil {
		return nil, nil, err
	}
	cursors, err := readCursors(filepath.Join(dir, cursorsFile))
	if err != nil {
		return nil, nil, err
	}
	pending := func(reader string) int {
		c := cursors[reader]
		if c >= uint64(len(events)) {
			return 0
		}
		return len(unread(events[c:], reader))
	}
	return cursors, pending, nil
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
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return Event{}, err
	}
	if err := syncFile(l.file); err != nil {
		return Event{}, err
	}
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
	return l.cursors[reader]
}

func (l *Log) Ack(reader string, seq uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq > uint64(len(l.events)) {
		return fmt.Errorf("ack %d beyond last event %d", seq, len(l.events))
	}
	if seq <= l.cursors[reader] {
		return nil
	}
	l.cursors[reader] = seq
	close(l.acked)
	l.acked = make(chan struct{})
	return writeCursors(filepath.Join(l.dir, cursorsFile), l.cursors)
}

// Cursors returns every reader's cursor and a channel closed when the next one advances.
func (l *Log) Cursors() (map[string]uint64, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.cursors), l.acked
}

// Pending returns the events a reader has not acknowledged, leaving out presence events and the reader's own.
func (l *Log) Pending(reader string) []Event {
	events, _ := l.After(l.Cursor(reader))
	return unread(events, reader)
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

func readCursors(path string) (map[string]uint64, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]uint64{}, nil
	}
	if err != nil {
		return nil, err
	}
	cursors := map[string]uint64{}
	if err := json.Unmarshal(b, &cursors); err != nil {
		return nil, err
	}
	return cursors, nil
}

func writeCursors(path string, cursors map[string]uint64) error {
	b, err := json.Marshal(cursors)
	if err != nil {
		return err
	}
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
	if err := os.Rename(tmp, path); err != nil {
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
