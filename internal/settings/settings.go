// Package settings holds the reader's preferences, shared by every browser that opens kapi.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sync"

	"github.com/xaaha/kapi/internal/page"
)

const (
	System = "system"
	Light  = "light"
	Dark   = "dark"

	DefaultTheme = "default"
	MaxRecent    = 3

	ArchiveNotice = "archive-intro"
)

var (
	Modes   = []string{System, Light, Dark}
	Notices = []string{ArchiveNotice}
)

type Settings struct {
	Mode      string    `json:"mode"`
	Theme     string    `json:"theme"`
	Recent    []string  `json:"recent"`
	Retention Retention `json:"retention"`
	Dismissed []string  `json:"dismissed"`
}

type Patch struct {
	Mode      *string         `json:"mode,omitempty"`
	Theme     *string         `json:"theme,omitempty"`
	Retention *RetentionPatch `json:"retention,omitempty"`
	Dismiss   *string         `json:"dismiss,omitempty"`
}

var ErrInvalid = errors.New("invalid setting")

type Store interface {
	Get(ctx context.Context) (Settings, error)
	Update(ctx context.Context, p Patch) (Settings, error)
}

func Default() Settings {
	return Settings{Mode: System, Theme: DefaultTheme, Recent: []string{DefaultTheme}, Retention: DefaultRetention(), Dismissed: []string{}}
}

type File struct {
	path   string
	known  func(theme string) bool
	logf   func(format string, args ...any)
	warned map[string]bool
	mu     sync.Mutex
}

func NewFile(path string, known func(theme string) bool) *File {
	return &File{path: path, known: known, logf: log.Printf, warned: map[string]bool{}}
}

func (f *File) Get(context.Context) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read()
}

func (f *File) Update(_ context.Context, p Patch) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.Mode != nil && !slices.Contains(Modes, *p.Mode) {
		return Settings{}, fmt.Errorf("%w: mode %q is not one of %v", ErrInvalid, *p.Mode, Modes)
	}
	if p.Theme != nil && !f.known(*p.Theme) {
		return Settings{}, fmt.Errorf("%w: no theme named %q", ErrInvalid, *p.Theme)
	}
	if err := p.Retention.validate(); err != nil {
		return Settings{}, err
	}
	if p.Dismiss != nil && !slices.Contains(Notices, *p.Dismiss) {
		return Settings{}, fmt.Errorf("%w: no notice named %q", ErrInvalid, *p.Dismiss)
	}
	s, err := f.read()
	if err != nil {
		return Settings{}, err
	}
	if p.Mode != nil {
		s.Mode = *p.Mode
	}
	if p.Theme != nil {
		s.Theme = *p.Theme
		s.Recent = recent(*p.Theme, s.Recent)
	}
	s.Retention = p.Retention.apply(s.Retention)
	if p.Dismiss != nil && !slices.Contains(s.Dismissed, *p.Dismiss) {
		s.Dismissed = append(s.Dismissed, *p.Dismiss)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	if err := page.WriteAtomic(f.path, append(b, '\n')); err != nil {
		return Settings{}, err
	}
	return s, nil
}

func (f *File) read() (Settings, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	var stored struct {
		Mode      string          `json:"mode"`
		Theme     string          `json:"theme"`
		Recent    []string        `json:"recent"`
		Retention json.RawMessage `json:"retention"`
		Dismissed []string        `json:"dismissed"`
	}
	if err := json.Unmarshal(b, &stored); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", f.path, err)
	}
	s := Settings{Mode: stored.Mode, Theme: stored.Theme, Recent: stored.Recent, Retention: f.retention(stored.Retention), Dismissed: stored.Dismissed}
	f.clean(&s)
	return s, nil
}

func (f *File) clean(s *Settings) {
	if !slices.Contains(Modes, s.Mode) {
		s.Mode = System
	}
	if !f.known(s.Theme) {
		s.Theme = DefaultTheme
	}
	kept := slices.DeleteFunc(slices.Clone(s.Recent), func(id string) bool { return !f.known(id) })
	s.Recent = recent(s.Theme, kept)
	s.Dismissed = slices.DeleteFunc(slices.Clone(s.Dismissed), func(id string) bool { return !slices.Contains(Notices, id) })
	if s.Dismissed == nil {
		s.Dismissed = []string{}
	}
}

func recent(theme string, before []string) []string {
	out := []string{theme}
	for _, id := range before {
		if len(out) == MaxRecent {
			break
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
