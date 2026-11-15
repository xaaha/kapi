package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/xaaha/cli"

	pkgpage "github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/render"
	"github.com/xaaha/kapi/internal/settings"
)

const maxSlug = 80

func newNewCmd(c api, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "Print the URL without opening a browser")
	return &cli.Command{
		Name:  "new",
		Short: "Create a draft page, print its path, and open it",
		Long: "Create a page titled <title> in the drafts folder (~/kapi/drafts unless the library setting moves it), print its path,\n" +
			"and open it like kapi open. The file name is a slug of the title; when that name is taken, \" 2\", \" 3\" and so on\n" +
			"are added, so an existing file is never overwritten. Drafts are temporary: the person saves the ones worth keeping\n" +
			"into the library from the page's menu. Saving moves the file, and kapi wait on the old path exits naming the new one.",
		Flags: fs,
		Args:  []cli.ArgDef{{Name: "title", Required: true, Desc: "Title of the page; quoting the words is optional"}},
		Examples: []cli.Example{
			{Command: `kapi new "Rollout plan"`, Description: "Create and open ~/kapi/drafts/rollout-plan.html"},
			{Command: "kapi new --print Auth review", Description: "Create auth-review.html and only print its path and URL"},
		},
		Run: func(args []string) error {
			title := strings.Join(strings.Fields(strings.Join(args, " ")), " ")
			if title == "" {
				return fmt.Errorf("expected a title\nSee 'kapi new --help' for usage")
			}
			path, err := createDraft(home(), title)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, path); err != nil {
				return err
			}
			return openPage(c, out, path, *printOnly)
		},
	}
}

func createDraft(kapiHome, title string) (string, error) {
	prefs, err := settings.NewFile(filepath.Join(kapiHome, "settings.json"), render.HasPalette).Get(context.Background())
	if err != nil {
		return "", err
	}
	folders, err := prefs.Folders()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(folders.Drafts, 0o755); err != nil { //nolint:gosec // drafts are the person's own pages
		return "", err
	}
	body := []byte("<h1>" + html.EscapeString(title) + "</h1>\n")
	for n := 1; ; n++ {
		path := filepath.Join(folders.Drafts, pkgpage.Numbered(slug(title)+".html", n))
		if exists(pkgpage.FilesDir(path)) || exists(filepath.Join(kapiHome, "pages", pkgpage.ID(path))) {
			continue
		}
		err := writeNew(path, body)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return path, err
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

func slug(title string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(title) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}
		next := string(r)
		if gap && b.Len() > 0 {
			next = "-" + next
		}
		if b.Len()+len(next) > maxSlug {
			break
		}
		b.WriteString(next)
		gap = false
	}
	if b.Len() == 0 {
		return "untitled"
	}
	return b.String()
}
