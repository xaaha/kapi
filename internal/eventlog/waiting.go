package eventlog

import "path/filepath"

func anyHuman(events []Event) bool {
	for i := range events {
		if events[i].Author == "human" {
			return true
		}
	}
	return false
}

// Waiting reports whether a person wrote something reader has not seen.
func (l *Log) Waiting(reader string) bool {
	return anyHuman(l.Pending(reader))
}

// WaitingIn is Waiting for a log that is not open.
func WaitingIn(dir, reader string) (bool, error) {
	events, _, err := readEvents(filepath.Join(dir, eventsFile))
	if err != nil {
		return false, err
	}
	pos, err := readPositions(dir)
	if err != nil {
		return false, err
	}
	seen := min(pos.Seen[reader], uint64(len(events)))
	return anyHuman(unread(events[seen:], reader, Ended(events))), nil
}
