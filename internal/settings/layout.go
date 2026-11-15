package settings

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxLayoutEntries = 1000
	MaxOrder         = 1000
	MaxTitle         = 200
	maxFolderKey     = 4096
)

type Layout struct {
	FolderTitles map[string]string   `json:"folderTitles"`
	PageTitles   map[string]string   `json:"pageTitles"`
	Order        map[string][]string `json:"order"`
	Collapsed    map[string]bool     `json:"collapsed"`
}

type LayoutPatch struct {
	FolderTitles map[string]*string  `json:"folderTitles,omitempty"`
	PageTitles   map[string]*string  `json:"pageTitles,omitempty"`
	Order        map[string][]string `json:"order,omitempty"`
	Collapsed    map[string]bool     `json:"collapsed,omitempty"`
}

type Places struct {
	Folders map[string]bool
	Pages   map[string]bool
}

func EmptyLayout() Layout {
	return Layout{FolderTitles: map[string]string{}, PageTitles: map[string]string{}, Order: map[string][]string{}, Collapsed: map[string]bool{}}
}

func (p *LayoutPatch) empty() bool {
	return p == nil || len(p.FolderTitles)+len(p.PageTitles)+len(p.Order)+len(p.Collapsed) == 0
}

func (p *LayoutPatch) validate(known Places) error {
	if p == nil {
		return nil
	}
	for folder, title := range p.FolderTitles {
		if err := checkFolderTitle(known, folder, title); err != nil {
			return err
		}
	}
	for id, title := range p.PageTitles {
		if err := checkPageTitle(known, id, title); err != nil {
			return err
		}
	}
	for folder, ids := range p.Order {
		if err := checkOrder(known, folder, ids); err != nil {
			return err
		}
	}
	for folder := range p.Collapsed {
		if err := checkFolder(known, folder); err != nil {
			return err
		}
	}
	return nil
}

func (l *Layout) apply(p *LayoutPatch) error {
	if p == nil {
		return nil
	}
	for folder, title := range p.FolderTitles {
		setOrDelete(l.FolderTitles, folder, cleanTitle(title))
	}
	for id, title := range p.PageTitles {
		setOrDelete(l.PageTitles, id, cleanTitle(title))
	}
	for folder, ids := range p.Order {
		if len(ids) == 0 {
			delete(l.Order, folder)
		} else {
			l.Order[folder] = ids
		}
	}
	for folder, collapsed := range p.Collapsed {
		if collapsed {
			l.Collapsed[folder] = true
		} else {
			delete(l.Collapsed, folder)
		}
	}
	for name, n := range map[string]int{"folderTitles": len(l.FolderTitles), "pageTitles": len(l.PageTitles), "order": len(l.Order), "collapsed": len(l.Collapsed)} {
		if n > MaxLayoutEntries {
			return fmt.Errorf("%w: layout.%s would hold %d entries, over the limit of %d", ErrInvalid, name, n, MaxLayoutEntries)
		}
	}
	return nil
}

func (l *Layout) adopt(p *LayoutPatch, known Places) {
	if p == nil {
		return
	}
	for folder, title := range p.FolderTitles {
		if cleanTitle(title) != "" && checkFolderTitle(known, folder, title) == nil {
			addMissing(l.FolderTitles, folder, cleanTitle(title))
		}
	}
	for id, title := range p.PageTitles {
		if cleanTitle(title) != "" && checkPageTitle(known, id, title) == nil {
			addMissing(l.PageTitles, id, cleanTitle(title))
		}
	}
	for folder, ids := range p.Order {
		if len(ids) > 0 && checkOrder(known, folder, ids) == nil {
			addMissing(l.Order, folder, ids)
		}
	}
	for folder, collapsed := range p.Collapsed {
		if collapsed && checkFolder(known, folder) == nil {
			addMissing(l.Collapsed, folder, true)
		}
	}
}

func setOrDelete(m map[string]string, key, value string) {
	if value == "" {
		delete(m, key)
	} else {
		m[key] = value
	}
}

func addMissing[V any](m map[string]V, key string, value V) {
	if _, taken := m[key]; taken || len(m) >= MaxLayoutEntries {
		return
	}
	m[key] = value
}

func cleanTitle(title *string) string {
	if title == nil {
		return ""
	}
	return strings.TrimSpace(*title)
}

func checkFolder(known Places, folder string) error {
	if len(folder) > maxFolderKey || !known.Folders[folder] {
		return fmt.Errorf("%w: kapi knows no folder %q", ErrInvalid, truncate(folder))
	}
	return nil
}

func checkPage(known Places, id string) error {
	if !known.Pages[id] {
		return fmt.Errorf("%w: kapi knows no page %q", ErrInvalid, truncate(id))
	}
	return nil
}

func checkFolderTitle(known Places, folder string, title *string) error {
	if err := checkFolder(known, folder); err != nil {
		return err
	}
	return checkTitle(title)
}

func checkPageTitle(known Places, id string, title *string) error {
	if err := checkPage(known, id); err != nil {
		return err
	}
	return checkTitle(title)
}

func checkTitle(title *string) error {
	t := cleanTitle(title)
	switch {
	case !utf8.ValidString(t):
		return fmt.Errorf("%w: a layout title is not UTF-8", ErrInvalid)
	case utf8.RuneCountInString(t) > MaxTitle:
		return fmt.Errorf("%w: a layout title is longer than %d characters", ErrInvalid, MaxTitle)
	case strings.ContainsFunc(t, unicode.IsControl):
		return fmt.Errorf("%w: a layout title holds a control character", ErrInvalid)
	}
	return nil
}

func checkOrder(known Places, folder string, ids []string) error {
	if err := checkFolder(known, folder); err != nil {
		return err
	}
	if len(ids) > MaxOrder {
		return fmt.Errorf("%w: the order for %q lists %d pages, over the limit of %d", ErrInvalid, truncate(folder), len(ids), MaxOrder)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !isPageID(id) || seen[id] {
			return fmt.Errorf("%w: the order for %q lists %q twice or it is not a page id", ErrInvalid, truncate(folder), truncate(id))
		}
		seen[id] = true
	}
	return nil
}

func isPageID(id string) bool {
	return len(id) == 16 && strings.Trim(id, "0123456789abcdef") == ""
}

func truncate(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "..."
}

func (f *File) layout(raw json.RawMessage) Layout {
	out := EmptyLayout()
	if raw == nil {
		return out
	}
	var stored Layout
	if err := json.Unmarshal(raw, &stored); err != nil {
		f.warn("layout", fmt.Sprintf("%.80s is not a layout object", raw), "an empty layout")
		return out
	}
	for folder, title := range stored.FolderTitles {
		if checkTitle(&title) == nil {
			setOrDelete(out.FolderTitles, folder, strings.TrimSpace(title))
		}
	}
	for id, title := range stored.PageTitles {
		if isPageID(id) && checkTitle(&title) == nil {
			setOrDelete(out.PageTitles, id, strings.TrimSpace(title))
		}
	}
	for folder, ids := range stored.Order {
		if len(ids) > 0 && checkOrder(Places{Folders: map[string]bool{folder: true}}, folder, ids) == nil {
			out.Order[folder] = ids
		}
	}
	maps.DeleteFunc(stored.Collapsed, func(_ string, v bool) bool { return !v })
	maps.Copy(out.Collapsed, stored.Collapsed)
	return out
}
