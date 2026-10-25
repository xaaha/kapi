package attach

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"
)

const (
	MaxBytes     = 10 << 20
	MaxPerSend   = 4
	MaxSendBytes = 25 << 20
	dirName      = "files"
)

var (
	ErrType    = errors.New("only PNG, JPEG, GIF and WebP images can be attached")
	ErrTooBig  = fmt.Errorf("an image can be at most %d MB", MaxBytes>>20)
	ErrFull    = errors.New("attachments are using all the space kapi allows; wait for old ones to expire, then try again")
	ErrMissing = errors.New("that image is not on this page; it may have expired")
)

var exts = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

var namePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

type File struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

// Store keeps images under <root>/<page id>/files; a file's modification time is when it was last used.
type Store struct {
	root    string
	now     func() time.Time
	ttl     time.Duration
	maxDisk int64
	grace   time.Duration
	mu      sync.Mutex
}

func New(root string) *Store {
	return &Store{root: root, now: time.Now, ttl: 7 * 24 * time.Hour, maxDisk: 512 << 20, grace: time.Hour}
}

func (s *Store) dir(page string) string {
	return filepath.Join(s.root, page, dirName)
}

func (s *Store) Put(page string, data []byte) (File, error) {
	if len(data) > MaxBytes {
		return File{}, ErrTooBig
	}
	mime := http.DetectContentType(data)
	ext, ok := exts[mime]
	if !ok {
		return File{}, ErrType
	}
	sum := sha256.Sum256(data)
	f := File{Name: hex.EncodeToString(sum[:16]) + ext, Mime: mime, Size: int64(len(data))}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir(page), f.Name)
	if _, err := os.Stat(path); err == nil {
		return f, s.touch(path)
	}
	if err := s.makeRoom(f.Size); err != nil {
		return File{}, err
	}
	if err := os.MkdirAll(s.dir(page), 0o700); err != nil {
		return File{}, err
	}
	tmp, err := os.CreateTemp(s.dir(page), ".upload-*")
	if err != nil {
		return File{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return File{}, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return File{}, err
	}
	if err := tmp.Close(); err != nil {
		return File{}, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return File{}, err
	}
	return f, s.touch(path)
}

func (s *Store) Path(page, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", ErrMissing
	}
	path := filepath.Join(s.dir(page), name)
	if _, err := os.Stat(path); err != nil {
		return "", ErrMissing
	}
	return path, nil
}

func (s *Store) Use(page string, names []string) ([]string, error) {
	if len(names) > MaxPerSend {
		return nil, fmt.Errorf("at most %d images per note or message", MaxPerSend)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := make([]string, 0, len(names))
	var total int64
	for _, name := range names {
		path, err := s.Path(page, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		total += info.Size()
		paths = append(paths, path)
	}
	if total > MaxSendBytes {
		return nil, fmt.Errorf("images on one note or message can be at most %d MB together", MaxSendBytes>>20)
	}
	for _, path := range paths {
		if err := s.touch(path); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func (s *Store) touch(path string) error {
	now := s.now()
	return os.Chtimes(path, now, now)
}

type stored struct {
	path string
	size int64
	used time.Time
}

func (s *Store) all() ([]stored, error) {
	dirs, err := filepath.Glob(filepath.Join(s.root, "*", dirName))
	if err != nil {
		return nil, err
	}
	var out []stored
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !namePattern.MatchString(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, stored{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		}
	}
	slices.SortFunc(out, func(a, b stored) int { return a.used.Compare(b.used) })
	return out, nil
}

func (s *Store) Sweep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.makeRoom(0); !errors.Is(err, ErrFull) {
		return err
	}
	return nil
}

func (s *Store) makeRoom(need int64) error {
	files, err := s.all()
	if err != nil {
		return err
	}
	now := s.now()
	var total int64
	kept := files[:0]
	for _, f := range files {
		if now.Sub(f.used) > s.ttl {
			if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		total += f.size
		kept = append(kept, f)
	}
	for _, f := range kept {
		if total+need <= s.maxDisk {
			break
		}
		if now.Sub(f.used) < s.grace {
			break
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		total -= f.size
	}
	if total+need > s.maxDisk {
		return ErrFull
	}
	return nil
}
