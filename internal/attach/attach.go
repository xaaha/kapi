package attach

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	MaxBytes   = 10 << 20
	MaxPerSend = 4
	dirName    = "files"
	tempPrefix = ".upload-"
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
	dir := s.dir(page)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return File{}, err
	}
	tmp, err := writeTemp(dir, data)
	if err != nil {
		return File{}, err
	}
	defer os.Remove(tmp)
	if err := s.touch(tmp); err != nil {
		return File{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(dir, f.Name)
	if _, err := os.Stat(path); err == nil {
		return f, s.touch(path)
	}
	if err := s.makeRoom(f.Size); err != nil {
		return File{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return File{}, err
	}
	if err := syncDir(dir); err != nil {
		return File{}, err
	}
	return f, s.touch(path)
}

func writeTemp(dir string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
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

func (s *Store) Use(page string, names []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := make(map[string]string, len(names))
	for _, name := range names {
		path, err := s.Path(page, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		paths[name] = path
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

func (s *Store) all() []stored {
	dirs, _ := filepath.Glob(filepath.Join(s.root, "*", dirName))
	now := s.now()
	var out []stored
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				log.Printf("attachments: skipping %s: %v", dir, err)
			}
			continue
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if strings.HasPrefix(e.Name(), tempPrefix) && now.Sub(info.ModTime()) > s.grace {
				os.Remove(path)
				continue
			}
			if !namePattern.MatchString(e.Name()) {
				continue
			}
			if info.ModTime().After(now) {
				if err := os.Chtimes(path, now, now); err != nil {
					log.Printf("attachments: %v", err)
				}
			}
			out = append(out, stored{path, info.Size(), info.ModTime()})
		}
	}
	slices.SortFunc(out, func(a, b stored) int { return a.used.Compare(b.used) })
	return out
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
	files := s.all()
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
