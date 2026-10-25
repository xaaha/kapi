package attach

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const page = "0123456789abcdef"

var pngBytes, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

func image(t *testing.T, kind string, size int) []byte {
	t.Helper()
	heads := map[string][]byte{
		"png":  pngBytes,
		"jpeg": {0xff, 0xd8, 0xff, 0xe0},
		"gif":  []byte("GIF89a"),
		"webp": []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
	}
	b := append([]byte{}, heads[kind]...)
	return append(b, bytes.Repeat([]byte{kind[0]}, max(0, size-len(b)))...)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := &clock{time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	s := New(t.TempDir())
	s.now = c.now
	return s, c
}

func TestPutStoresOnlyPNGJPEGGIFAndWebPUnderTheirHash(t *testing.T) {
	s, _ := newStore(t)
	for kind, ext := range map[string]string{"png": ".png", "jpeg": ".jpg", "gif": ".gif", "webp": ".webp"} {
		f, err := s.Put(page, image(t, kind, 100))
		if err != nil || !strings.HasSuffix(f.Name, ext) || len(f.Name) != 32+len(ext) {
			t.Errorf("%s: %+v, %v", kind, f, err)
			continue
		}
		path, err := s.Path(page, f.Name)
		if got, _ := os.ReadFile(path); err != nil || !bytes.Equal(got, image(t, kind, 100)) {
			t.Errorf("%s: stored %d bytes at %q, %v", kind, len(got), path, err)
		}
	}
	for name, data := range map[string][]byte{
		"svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html": []byte("<html><body>hi</body></html>"),
		"text": []byte("just text"),
	} {
		if _, err := s.Put(page, data); !errors.Is(err, ErrType) {
			t.Errorf("%s: err = %v, want ErrType", name, err)
		}
	}
	if _, err := s.Put(page, image(t, "png", MaxBytes+1)); !errors.Is(err, ErrTooBig) {
		t.Errorf("too big: err = %v", err)
	}
	a, _ := s.Put(page, image(t, "png", 100))
	b, _ := s.Put(page, image(t, "png", 100))
	if a.Name != b.Name {
		t.Errorf("the same image stored twice: %s and %s", a.Name, b.Name)
	}
	if entries, _ := os.ReadDir(s.dir(page)); len(entries) != 4 {
		t.Errorf("files on disk: %d, want 4 and no temp files", len(entries))
	}
}

func TestPathRefusesNamesThatAreNotStoredImages(t *testing.T) {
	s, _ := newStore(t)
	f, _ := s.Put(page, image(t, "png", 10))
	if err := os.WriteFile(filepath.Join(s.root, page, "page.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../page.txt", "page.txt", strings.ToUpper(f.Name), f.Name + "x", "0123456789abcdef0123456789abcdef.svg", "00000000000000000000000000000000.png"} {
		if _, err := s.Path(page, name); !errors.Is(err, ErrMissing) {
			t.Errorf("Path(%q) = %v, want ErrMissing", name, err)
		}
	}
	if _, err := s.Path("fedcba9876543210", f.Name); !errors.Is(err, ErrMissing) {
		t.Errorf("another page's image: %v", err)
	}
}

func TestUseMarksImagesUsedAndKeepsThePerSendLimits(t *testing.T) {
	s, c := newStore(t)
	var names []string
	for i := range MaxPerSend + 1 {
		f, err := s.Put(page, image(t, "png", 50+i))
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, f.Name)
	}
	if _, err := s.Use(page, names); err == nil {
		t.Error("five images on one note were accepted")
	}
	c.t = c.t.Add(time.Hour)
	paths, err := s.Use(page, names[:2])
	if err != nil || len(paths) != 2 {
		t.Fatalf("Use: %v, %v", paths, err)
	}
	if info, _ := os.Stat(paths[0]); !info.ModTime().Equal(c.t) {
		t.Errorf("used at %v, want %v", info.ModTime(), c.t)
	}
	if _, err := s.Use(page, []string{names[0], "ffffffffffffffffffffffffffffffff.png"}); !errors.Is(err, ErrMissing) {
		t.Errorf("a missing image: %v", err)
	}
	big := []string{}
	for i := range 3 {
		f, err := s.Put(page, image(t, "jpeg", MaxBytes-i))
		if err != nil {
			t.Fatal(err)
		}
		big = append(big, f.Name)
	}
	if _, err := s.Use(page, big); err == nil {
		t.Error("30 MB of images on one note was accepted")
	}
}

func TestSweepDeletesImagesUnusedForAWeek(t *testing.T) {
	s, c := newStore(t)
	old, _ := s.Put(page, image(t, "png", 10))
	c.t = c.t.Add(6 * 24 * time.Hour)
	kept, _ := s.Put("fedcba9876543210", image(t, "gif", 10))
	if _, err := s.Use(page, []string{old.Name}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(6 * 24 * time.Hour)
	stale, _ := s.Put(page, image(t, "jpeg", 10))
	c.t = c.t.Add(7*24*time.Hour + time.Minute)
	if _, err := s.Use(page, []string{stale.Name}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Path(page, old.Name); !errors.Is(err, ErrMissing) {
		t.Error("an image unused for over a week is still there")
	}
	if _, err := s.Path("fedcba9876543210", kept.Name); !errors.Is(err, ErrMissing) {
		t.Error("another page's stale image is still there")
	}
	if _, err := s.Path(page, stale.Name); err != nil {
		t.Errorf("an image used just now was deleted: %v", err)
	}
}

func TestADiskCapEvictsTheOldestButNeverAFreshImage(t *testing.T) {
	s, c := newStore(t)
	s.maxDisk = 300
	a, _ := s.Put(page, image(t, "png", 100))
	c.t = c.t.Add(2 * time.Hour)
	b, _ := s.Put(page, image(t, "gif", 100))
	c.t = c.t.Add(2 * time.Hour)
	d, _ := s.Put(page, image(t, "jpeg", 100))
	c.t = c.t.Add(2 * time.Hour)
	e, err := s.Put(page, image(t, "webp", 100))
	if err != nil {
		t.Fatalf("an upload that fits once the oldest goes: %v", err)
	}
	if _, err := s.Path(page, a.Name); !errors.Is(err, ErrMissing) {
		t.Error("the oldest image was not evicted")
	}
	for _, f := range []File{b, d, e} {
		if _, err := s.Path(page, f.Name); err != nil {
			t.Errorf("%s was evicted: %v", f.Name, err)
		}
	}
	s.maxDisk = 250
	c.t = c.t.Add(10 * time.Minute)
	if _, err := s.Put(page, image(t, "png", 200)); !errors.Is(err, ErrFull) {
		t.Errorf("an upload that only fits by evicting images under an hour old: %v, want ErrFull", err)
	}
	if _, err := s.Path(page, e.Name); err != nil {
		t.Errorf("an image added in the last hour was evicted: %v", err)
	}
}
