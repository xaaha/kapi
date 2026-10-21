package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
}

const (
	eventsFile  = "events.ndjson"
	cursorsFile = "cursors.json"
)

func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	events, err := readEvents(filepath.Join(dir, eventsFile))
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
	return &Log{dir: dir, file: f, events: events, cursors: cursors, changed: make(chan struct{})}, nil
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

func (l *Log) Append(e Event) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	if err := l.file.Sync(); err != nil {
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
	out := make([]Event, len(l.events)-int(cursor))
	copy(out, l.events[cursor:])
	return out, l.changed
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
	return writeCursors(filepath.Join(l.dir, cursorsFile), l.cursors)
}

func (l *Log) Pending(reader string) []Event {
	events, _ := l.After(l.Cursor(reader))
	return events
}

func readEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, len(events)+1, err)
		}
		events = append(events, e)
	}
	return events, sc.Err()
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
	return cursors, json.Unmarshal(b, &cursors)
}

func writeCursors(path string, cursors map[string]uint64) error {
	b, err := json.Marshal(cursors)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
