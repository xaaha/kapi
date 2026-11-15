package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/xaaha/cli"

	"github.com/xaaha/kapi/internal/daemon"
)

var now = time.Now

func newHomeCmd(c api, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("home", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "Print the URL without opening a browser")
	return &cli.Command{
		Name:  "home",
		Short: "Open the notebook home page in the browser",
		Long:  "Print the notebook home page's local URL and open it in the default browser, unless KAPI_NO_BROWSER is set. Starts the daemon if it is not running. The URL holds a token, so treat it like a password.",
		Flags: fs,
		Examples: []cli.Example{
			{Command: "kapi home", Description: "Open every page kapi knows about in the browser"},
			{Command: "kapi home --print", Description: "Only print the URL"},
		},
		Run: func(args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("expected no arguments, got %d\nSee 'kapi home --help' for usage", len(args))
			}
			resp, err := c.Do(&daemon.Request{Op: "home"})
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, resp.URL); err != nil {
				return err
			}
			if *printOnly || os.Getenv("KAPI_NO_BROWSER") != "" {
				return nil
			}
			return openBrowser(resp.URL)
		},
	}
}

type listedPage struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Path    string    `json:"path"`
	State   string    `json:"state"`
	Viewed  time.Time `json:"viewed"`
	Touched time.Time `json:"touched"`
}

func newLsCmd(c api, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "Print a JSON array with id, title, path, state, and the viewed and touched times in UTC")
	return &cli.Command{
		Name:  "ls",
		Short: "List pages, most recently viewed first",
		Long:  "List every page kapi knows about whose file still exists, most recently viewed in a browser first, then most recently changed. Starts the daemon if it is not running.",
		Flags: fs,
		Examples: []cli.Example{
			{Command: "kapi ls", Description: "Title, state, when you last viewed it, and path"},
			{Command: "kapi ls --json", Description: "The same list for scripts and agents"},
		},
		Run: func(args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("expected no arguments, got %d\nSee 'kapi ls --help' for usage", len(args))
			}
			resp, err := c.Do(&daemon.Request{Op: "index"})
			if err != nil {
				return err
			}
			if *asJSON {
				pages := make([]listedPage, 0, len(resp.Index))
				for i := range resp.Index {
					p := &resp.Index[i]
					pages = append(pages, listedPage{ID: p.ID, Title: p.Title, Path: p.Path, State: p.State, Viewed: p.Viewed.UTC(), Touched: p.Touched.UTC()})
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(pages)
			}
			if len(resp.Index) == 0 {
				_, err := fmt.Fprintln(out, "No pages yet. Run kapi open <page> to make one.")
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TITLE\tSTATE\tVIEWED\tPATH")
			at := now()
			for i := range resp.Index {
				p := &resp.Index[i]
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", oneLine(p.Title), p.State, ago(p.Viewed, at), oneLine(p.Path))
			}
			return tw.Flush()
		},
	}
}

func oneLine(s string) string {
	if strings.ContainsAny(s, "\t\n\r") {
		return strconv.Quote(s)
	}
	return s
}

func ago(t, at time.Time) string {
	d := at.Sub(t)
	if d < time.Minute {
		return "just now"
	}
	for _, u := range []struct {
		name string
		size time.Duration
	}{
		{"year", 365 * 24 * time.Hour},
		{"month", 30 * 24 * time.Hour},
		{"week", 7 * 24 * time.Hour},
		{"day", 24 * time.Hour},
		{"hour", time.Hour},
		{"minute", time.Minute},
	} {
		if d >= u.size {
			n := int(d / u.size)
			if n == 1 {
				return "1 " + u.name + " ago"
			}
			return strconv.Itoa(n) + " " + u.name + "s ago"
		}
	}
	return "just now"
}
