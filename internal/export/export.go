package export

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xaaha/kapi/internal/attach"
	"github.com/xaaha/kapi/internal/render"
)

type File struct {
	Name string
	Type string
	Data []byte
}

var pageImage = regexp.MustCompile(`^\./[^/]+\.files/([^/]+)$`)

const imagesDir = "images"

func Markdown(pageName string, src []byte, filesDir string) (File, error) {
	base := strings.TrimSuffix(pageName, filepath.Ext(pageName))
	var names []string
	images := map[string][]byte{}
	md, err := render.Markdown(src, func(src string) string {
		m := pageImage.FindStringSubmatch(src)
		if len(m) < 2 || !attach.ValidName(m[1]) {
			return src
		}
		if _, ok := images[m[1]]; !ok {
			data, err := os.ReadFile(filepath.Join(filesDir, m[1]))
			if err != nil {
				return src
			}
			images[m[1]] = data
			names = append(names, m[1])
		}
		return imagesDir + "/" + m[1]
	})
	if err != nil {
		return File{}, err
	}
	if len(names) == 0 {
		return File{Name: base + ".md", Type: "text/markdown; charset=utf-8", Data: md}, nil
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	now := time.Now()
	add := func(name string, data []byte, method uint16) error {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: now})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := add(base+".md", md, zip.Deflate); err != nil {
		return File{}, err
	}
	for _, name := range names {
		if err := add(imagesDir+"/"+name, images[name], zip.Store); err != nil {
			return File{}, err
		}
	}
	if err := z.Close(); err != nil {
		return File{}, err
	}
	return File{Name: base + ".zip", Type: "application/zip", Data: buf.Bytes()}, nil
}
