package eventlog

import "path/filepath"

func waiting(events []Event, seen map[string]uint64) bool {
	var furthest uint64
	for _, at := range seen {
		furthest = max(furthest, at)
	}
	for i := min(furthest, uint64(len(events))); i < uint64(len(events)); i++ {
		if events[i].Author == "human" {
			return true
		}
	}
	return false
}

// Waiting reports whether a person wrote something no agent reader has seen.
func (l *Log) Waiting() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return waiting(l.events, l.pos.Seen)
}

// WaitingIn is Waiting for a log that is not open.
func WaitingIn(dir string) (bool, error) {
	events, _, err := readEvents(filepath.Join(dir, eventsFile))
	if err != nil {
		return false, err
	}
	pos, err := readPositions(dir)
	if err != nil {
		return false, err
	}
	return waiting(events, pos.Seen), nil
}
