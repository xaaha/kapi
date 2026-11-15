package page

import (
	"fmt"
	"html"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type MovedError struct {
	From, To string
}

func (e *MovedError) Error() string {
	return fmt.Sprintf("%s moved to %s when it was saved; use the new path from now on, e.g. kapi wait %q", e.From, e.To, e.To)
}

func (e *MovedError) Is(target error) bool { return target == fs.ErrNotExist }

var imgSrc = regexp.MustCompile(`(<img\b[^>]*\ssrc=")\./([^"/]*)/`)

func RenameImageRefs(src []byte, from, to string) []byte {
	return imgSrc.ReplaceAllFunc(src, func(m []byte) []byte {
		parts := imgSrc.FindSubmatch(m)
		dir, err := url.PathUnescape(html.UnescapeString(string(parts[2])))
		if err != nil || dir != filesName(from) {
			return m
		}
		out := append([]byte{}, parts[1]...)
		return append(append(out, "./"...), url.PathEscape(filesName(to))+"/"...)
	})
}

func Numbered(name string, n int) string {
	if n == 1 {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + " " + strconv.Itoa(n) + ext
}
