package settings

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const (
	DefaultArchiveAfter  = "24h"
	DefaultDeleteAfter   = "720h"
	DefaultKeepWhileOpen = true
)

type Retention struct {
	ArchiveAfter  string `json:"archiveAfter"`
	DeleteAfter   string `json:"deleteAfter"`
	KeepWhileOpen bool   `json:"keepWhileOpen"`
}

type RetentionPatch struct {
	ArchiveAfter  *string `json:"archiveAfter,omitempty"`
	DeleteAfter   *string `json:"deleteAfter,omitempty"`
	KeepWhileOpen *bool   `json:"keepWhileOpen,omitempty"`
}

type Policy struct {
	ArchiveAfter  time.Duration
	DeleteAfter   time.Duration
	KeepWhileOpen bool
}

func DefaultRetention() Retention {
	return Retention{ArchiveAfter: DefaultArchiveAfter, DeleteAfter: DefaultDeleteAfter, KeepWhileOpen: DefaultKeepWhileOpen}
}

func (r Retention) Policy() Policy {
	return Policy{
		ArchiveAfter:  durationOr(r.ArchiveAfter, DefaultArchiveAfter),
		DeleteAfter:   durationOr(r.DeleteAfter, DefaultDeleteAfter),
		KeepWhileOpen: r.KeepWhileOpen,
	}
}

func durationOr(s, fallback string) time.Duration {
	if d, err := parseDuration(s); err == nil {
		return d
	}
	d, _ := parseDuration(fallback)
	return d
}

func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration like \"24h\" or \"90m\"; use \"0\" to turn it off", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative; use \"0\" to turn it off", s)
	}
	return d, nil
}

func (p *RetentionPatch) validate() error {
	if p == nil {
		return nil
	}
	fields := []struct {
		name  string
		value *string
	}{{"retention.archiveAfter", p.ArchiveAfter}, {"retention.deleteAfter", p.DeleteAfter}}
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		if _, err := parseDuration(*field.value); err != nil {
			return fmt.Errorf("%w: %s %w", ErrInvalid, field.name, err)
		}
	}
	return nil
}

func (p *RetentionPatch) apply(r Retention) Retention {
	if p == nil {
		return r
	}
	if p.ArchiveAfter != nil {
		r.ArchiveAfter = *p.ArchiveAfter
	}
	if p.DeleteAfter != nil {
		r.DeleteAfter = *p.DeleteAfter
	}
	if p.KeepWhileOpen != nil {
		r.KeepWhileOpen = *p.KeepWhileOpen
	}
	return r
}

func (f *File) retention(raw json.RawMessage) Retention {
	r := DefaultRetention()
	if raw == nil {
		return r
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		f.warn("retention", fmt.Sprintf("%s is not an object", raw), "the defaults")
		return r
	}
	r.ArchiveAfter = f.durationField(fields, "archiveAfter", DefaultArchiveAfter)
	r.DeleteAfter = f.durationField(fields, "deleteAfter", DefaultDeleteAfter)
	if v, ok := fields["keepWhileOpen"]; ok {
		if err := json.Unmarshal(v, &r.KeepWhileOpen); err != nil || string(v) == "null" {
			r.KeepWhileOpen = DefaultKeepWhileOpen
			f.warn("retention.keepWhileOpen", fmt.Sprintf("%s is not true or false", v), fmt.Sprint(DefaultKeepWhileOpen))
		}
	}
	return r
}

func (f *File) durationField(fields map[string]json.RawMessage, key, fallback string) string {
	v, ok := fields[key]
	if !ok {
		return fallback
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil || string(v) == "null" {
		f.warn("retention."+key, fmt.Sprintf("%s is not a string", v), strconv.Quote(fallback))
		return fallback
	}
	if _, err := parseDuration(s); err != nil {
		f.warn("retention."+key, err.Error(), strconv.Quote(fallback))
		return fallback
	}
	return s
}

func (f *File) warn(field, problem, fallback string) {
	key := field + ": " + problem
	if f.warned[key] {
		return
	}
	f.warned[key] = true
	f.logf("settings: %s: ignoring %s; using %s", f.path, key, fallback)
}
