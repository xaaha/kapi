package daemon

import (
	"cmp"
	"os"
	"path/filepath"

	"github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/web/app"
)

// Index lists every page kapi has opened, for the notebook.
func (s *Server) Index() ([]app.Page, error) {
	dirs, err := s.pageDirs()
	if err != nil {
		return nil, err
	}
	out := make([]app.Page, 0, len(dirs))
	for _, d := range dirs {
		if d.err != nil {
			continue
		}
		p := app.Page{ID: filepath.Base(d.dir), Path: d.path, Title: page.Title(d.path), Folders: s.openers(d.path), Ended: s.pageStatus(d.path, d.dir).Ended}
		if src, err := os.ReadFile(d.path); err != nil {
			p.Missing = true
		} else {
			title, summary := page.Preview(src)
			p.Title, p.Summary = cmp.Or(title, p.Title), summary
		}
		if info, err := os.Stat(d.path); err == nil {
			p.Changed = info.ModTime()
		}
		if info, err := os.Stat(filepath.Join(d.dir, "events.ndjson")); err == nil && info.ModTime().After(p.Changed) {
			p.Changed = info.ModTime()
		}
		out = append(out, p)
	}
	return out, nil
}
