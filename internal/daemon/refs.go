package daemon

import (
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	refAttr   = regexp.MustCompile(`(?i)\s(src|href|srcset)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	refCSSURL = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]*))\s*\)`)
	refScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

func (s *Server) usedByAnotherPage(path, folder string) (bool, error) {
	dirs, err := s.pageDirs()
	if err != nil {
		return false, err
	}
	for _, d := range dirs {
		if d.err != nil || d.path == path {
			continue
		}
		src, err := os.ReadFile(d.path)
		if err != nil {
			continue
		}
		if refersInto(src, filepath.Dir(d.path), folder) {
			return true, nil
		}
	}
	return false, nil
}

func refersInto(src []byte, base, folder string) bool {
	for _, ref := range references(string(src)) {
		if target, ok := localPath(ref, base); ok && inside(folder, target) {
			return true
		}
	}
	return false
}

func references(src string) []string {
	var out []string
	for _, m := range refAttr.FindAllStringSubmatch(src, -1) {
		value := html.UnescapeString(m[2] + m[3] + m[4])
		if !strings.EqualFold(m[1], "srcset") {
			out = append(out, value)
			continue
		}
		for candidate := range strings.SplitSeq(value, ",") {
			if fields := strings.Fields(candidate); len(fields) > 0 {
				out = append(out, fields[0])
			}
		}
	}
	for _, m := range refCSSURL.FindAllStringSubmatch(html.UnescapeString(src), -1) {
		out = append(out, m[1]+m[2]+m[3])
	}
	return out
}

func localPath(ref, base string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" || strings.HasPrefix(ref, "//") {
		return "", false
	}
	if rest, ok := strings.CutPrefix(ref, "file://"); ok {
		ref = rest
	} else if refScheme.MatchString(ref) && !filepath.IsAbs(filepath.FromSlash(ref)) {
		return "", false
	}
	unescaped, err := url.PathUnescape(ref)
	if err != nil {
		return "", false
	}
	p := filepath.FromSlash(unescaped)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), true
}

func inside(folder, path string) bool {
	rel, err := filepath.Rel(folder, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
