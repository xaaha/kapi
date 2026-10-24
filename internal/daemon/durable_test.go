package daemon

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/xaaha/kapi/internal/page"
)

func TestTheTokenFilesAndPagePathAreWrittenThroughASyncedRename(t *testing.T) {
	var mu sync.Mutex
	var written []string
	write0 := writeAtomic
	writeAtomic = func(path string, b []byte) error {
		mu.Lock()
		written = append(written, path)
		mu.Unlock()
		return write0(path, b)
	}
	defer func() { writeAtomic = write0 }()
	home, _, stop := serving(t)
	path := writePage(t, "<p>hi</p>\n")
	call(t, home, &Request{Op: "append", Page: path, Author: "human", Kind: "message", Text: "hi"})
	stop()
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{HTTPURLPath(home), filepath.Join(home, "previous-tokens"), filepath.Join(home, "pages", page.ID(path), "page.txt")} {
		if !slices.Contains(written, want) {
			t.Errorf("%s was not written through page.WriteAtomic; synced writes: %v", want, written)
		}
	}
}
