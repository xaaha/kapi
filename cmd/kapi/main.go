package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/xaaha/cli"

	"github.com/xaaha/kapi/internal/attach"
	"github.com/xaaha/kapi/internal/client"
	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/export"
	pkgpage "github.com/xaaha/kapi/internal/page"
	"github.com/xaaha/kapi/internal/publish"
	"github.com/xaaha/kapi/internal/setup"
)

type api interface {
	Do(req *daemon.Request) (daemon.Response, error)
	Watch(page, reader string, fn func(daemon.Response) error) error
}

func main() {
	root := newRoot(client.Client{Home: home()}, os.Stdin, os.Stdout, time.Sleep)
	if err := root.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kapi:", err)
		os.Exit(1)
	}
}

func home() string {
	if h := os.Getenv("KAPI_HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(h, ".kapi")
}

var pageArg = cli.ArgDef{Name: "page", Required: true, Desc: "HTML page the events belong to", Kind: cli.KindFile}

func newRoot(c api, in io.Reader, out io.Writer, sleep func(time.Duration)) *cli.Command {
	rootFlags := flag.NewFlagSet("kapi", flag.ContinueOnError)
	var printVersion bool
	rootFlags.BoolVar(&printVersion, "v", false, "Print the kapi version")
	rootFlags.BoolVar(&printVersion, "version", false, "Print the kapi version")
	root := &cli.Command{
		Name:   "kapi",
		Long:   "kapi: shared HTML pages for you and your agent.",
		Flags:  rootFlags,
		Config: &cli.Config{Out: out},
		Run: func([]string) error {
			if printVersion {
				_, err := fmt.Fprintln(out, resolveVersion(version, debug.ReadBuildInfo))
				return err
			}
			return homeView(c, out)
		},
		SubCommands: []*cli.Command{
			waitCmd(c, out, sleep),
			watchCmd(c, out, sleep),
			messageCmd(c, out, "say", "", "Reply on a page as the agent"),
			seenCmd(c, out),
			messageCmd(c, out, "send", "human", "Send a message on a page as the human"),
			pendingCmd(c, out),
			statusCmd(c, out),
			daemonCmd(),
			newPresetCmd(out),
			newPublishCmd(out),
			newExportCmd(out),
			attachCmd(c, out),
			newOpenCmd(c, out),
		},
	}
	root.SubCommands = append(root.SubCommands, versionCmd(out), completionCmd(root, out), setupCmd(in, out), hookContextCmd(c, out), hookMessagesCmd(c, in, out))
	return root
}

func watchCmd(c api, out io.Writer, sleep func(time.Duration)) *cli.Command {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	reader := fs.String("reader", "agent", "Reader name; each reader keeps its own position")
	return &cli.Command{
		Name:  "watch",
		Short: "Stream a page's events, one line each",
		Long: "Stream every unseen event on a page, one line each, and keep streaming. Reconnects if the daemon restarts.\n" +
			"A printed line shows as Queued on the page until you run kapi say --seen or kapi seen with its number, so a\n" +
			"restarted watch prints every unseen event again. Prefer kapi wait where lines can be delayed or dropped.",
		Flags: fs,
		Args:  []cli.ArgDef{pageArg},
		Examples: []cli.Example{
			{Command: "kapi watch plan.html", Description: "Stream events for the agent"},
		},
		Run: func(args []string) error {
			page, err := pagePath(args, 1, "watch")
			if err != nil {
				return err
			}
			if err := eventlog.CheckReader(*reader); err != nil {
				return err
			}
			return watch(c, page, *reader, out, sleep)
		},
	}
}

func watch(c api, page, reader string, out io.Writer, sleep func(time.Duration)) error {
	for {
		err := c.Watch(page, reader, func(r daemon.Response) error {
			if _, err := fmt.Fprintln(out, eventLine(r.Event)); err != nil {
				return err
			}
			if r.Event.Kind == eventlog.KindEnd {
				return client.ErrStopWatch
			}
			return nil
		})
		if errors.Is(err, client.ErrStopWatch) {
			return nil
		}
		if !errors.Is(err, client.ErrConnectionLost) {
			return err
		}
		fmt.Fprintln(os.Stderr, "kapi: daemon connection lost, reconnecting")
		sleep(500 * time.Millisecond)
	}
}

func waitCmd(c api, out io.Writer, sleep func(time.Duration)) *cli.Command {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	reader := fs.String("reader", "agent", "Reader name; each reader keeps its own position")
	return &cli.Command{
		Name:  "wait",
		Short: "Wait for a page's next event, print every unseen one, and exit",
		Long: "Block until the page has an event you have not been handed yet, print every unseen event, one line each,\n" +
			"and exit. Run it as a background command whose exit wakes you, and start it again each time it exits.\n" +
			"Printed events show as Queued on the page until you run kapi say --seen or kapi seen with its number.",
		Flags: fs,
		Args:  []cli.ArgDef{pageArg},
		Examples: []cli.Example{
			{Command: "kapi wait plan.html", Description: "Wait for the human's next message"},
		},
		Run: func(args []string) error {
			page, err := pagePath(args, 1, "wait")
			if err != nil {
				return err
			}
			if err := eventlog.CheckReader(*reader); err != nil {
				return err
			}
			for {
				resp, err := c.Do(&daemon.Request{Op: "wait", Page: page, Reader: *reader})
				if errors.Is(err, client.ErrConnectionLost) {
					fmt.Fprintln(os.Stderr, "kapi: daemon connection lost, reconnecting")
					sleep(500 * time.Millisecond)
					continue
				}
				if err != nil {
					return err
				}
				for i := range resp.Events {
					if _, err := fmt.Fprintln(out, eventLine(&resp.Events[i])); err != nil {
						return err
					}
				}
				if len(resp.Events) == 0 {
					continue
				}
				_, err = c.Do(&daemon.Request{Op: "queue", Page: page, Reader: *reader, Seq: resp.Events[len(resp.Events)-1].Seq})
				return err
			}
		},
	}
}

func seenCmd(c api, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("seen", flag.ContinueOnError)
	reader := fs.String("reader", "agent", "Reader name")
	return &cli.Command{
		Name:  "seen",
		Short: "Tell the page you read every event through the given number, without replying",
		Flags: fs,
		Args:  []cli.ArgDef{pageArg, {Name: "seq", Required: true, Desc: "Number of the last event you read, as printed: #12 or 12"}},
		Examples: []cli.Example{
			{Command: "kapi seen plan.html 12", Description: "You read every event through #12"},
		},
		Run: func(args []string) error {
			page, err := pagePath(args, 2, "seen")
			if err != nil {
				return err
			}
			if err := eventlog.CheckReader(*reader); err != nil {
				return err
			}
			seq, err := parseSeq(args[1])
			if err != nil {
				return err
			}
			resp, err := c.Do(&daemon.Request{Op: "seen", Page: page, Reader: *reader, Seq: seq})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "seen through #%d\n", resp.Event.Seq)
			return err
		},
	}
}

func parseSeq(s string) (uint64, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("want an event number such as #12, got %q", s)
	}
	return n, nil
}

func messageCmd(c api, out io.Writer, name, author, short string) *cli.Command {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	on := fs.String("on", "", "Element id the message is about")
	reader := &author
	seen := new(string)
	replyTo := new(string)
	if author == "" {
		reader = fs.String("reader", "agent", "Reader name to send as; readers with other names see the message")
		seen = fs.String("seen", "", "Number of the last event you read, as printed: #12 or 12; the page shows the human you read it")
		replyTo = fs.String("reply", "", "Notes this message answers, as printed: #12 or 12,13; the page marks them answered")
	}
	return &cli.Command{
		Name:       name,
		Short:      short,
		Long:       short + ". Prints the event number. Flags go before the page; every word after it is message text, and a word such as --on that names one of its flags is refused.",
		Flags:      fs,
		FlagsFirst: true,
		Args:       []cli.ArgDef{pageArg, {Name: "text", Required: true, Desc: "Message text; several words are joined"}},
		Examples: []cli.Example{
			{Command: "kapi " + name + ` --on cutover plan.html "is this still true?"`, Description: "Message about one element"},
		},
		Run: func(args []string) error {
			page, err := pagePath(args, 2, name)
			if err != nil {
				return err
			}
			for _, word := range args[1:] {
				flagName, _, _ := strings.Cut(strings.TrimPrefix(word, "--"), "=")
				if strings.HasPrefix(word, "--") && fs.Lookup(flagName) != nil {
					return fmt.Errorf("%s after the page would be sent as message text; put flags before the page: kapi %s %s ... %s <text>", word, name, word, args[0])
				}
			}
			if author == "" {
				if err := eventlog.CheckReader(*reader); err != nil {
					return err
				}
			}
			var seq uint64
			if *seen != "" {
				if seq, err = parseSeq(*seen); err != nil {
					return err
				}
			}
			var re []uint64
			if *replyTo != "" {
				for _, w := range strings.Split(*replyTo, ",") {
					n, err := parseSeq(strings.TrimSpace(w))
					if err != nil {
						return err
					}
					re = append(re, n)
				}
			}
			resp, err := c.Do(&daemon.Request{Op: "append", Page: page, Author: *reader, Kind: "message", Target: *on, Re: re, Text: strings.Join(args[1:], " "), Seq: seq})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "sent #%d\n", resp.Event.Seq)
			return err
		},
	}
}

func pendingCmd(c api, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("pending", flag.ContinueOnError)
	reader := fs.String("reader", "agent", "Reader name")
	return &cli.Command{
		Name:  "pending",
		Short: "List unseen events and mark them seen",
		Flags: fs,
		Args:  []cli.ArgDef{pageArg},
		Run: func(args []string) error {
			page, err := pagePath(args, 1, "pending")
			if err != nil {
				return err
			}
			if err := eventlog.CheckReader(*reader); err != nil {
				return err
			}
			resp, err := c.Do(&daemon.Request{Op: "pending", Page: page, Reader: *reader})
			if err != nil {
				return err
			}
			for i := range resp.Events {
				if _, err := fmt.Fprintln(out, eventLine(&resp.Events[i])); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func statusCmd(c api, out io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Short: "Show who is attached to a page",
		Flags: flag.NewFlagSet("status", flag.ContinueOnError),
		Args:  []cli.ArgDef{pageArg},
		Run: func(args []string) error {
			page, err := pagePath(args, 1, "status")
			if err != nil {
				return err
			}
			resp, err := c.Do(&daemon.Request{Op: "status", Page: page})
			if err != nil {
				return err
			}
			for _, r := range resp.Readers {
				state := "not attached"
				if r.Attached {
					state = "attached since " + r.Since.Local().Format("15:04:05")
				}
				if _, err := fmt.Fprintf(out, "%s: %s, %d unread\n", r.Reader, state, r.Pending); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func daemonCmd() *cli.Command {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	reopen := fs.Bool("reopen", false, "Reopen pages whose tab was open when the daemon last stopped; kapi sets this when it starts the daemon on demand")
	except := fs.String("reopen-except", "", "Page id not to reopen, because the kapi open that starts the daemon opens it itself")
	spawn := fs.Bool("spawn", false, "Start the daemon with --reopen in its own session, detached from this process, and exit; kapi runs this when it starts the daemon on demand")
	return &cli.Command{
		Name:  "daemon",
		Short: "Run the daemon in the foreground",
		Flags: fs,
		Long: "Run the daemon in the foreground. Other commands start it on demand, so this is only for debugging.\n" +
			"With --reopen, up to 5 pages whose browser tab was open when the daemon last stopped open again,\n" +
			"unless KAPI_NO_BROWSER, CI, SSH_CONNECTION or SSH_TTY is set or, on Linux, neither DISPLAY nor WAYLAND_DISPLAY is.",
		Run: func([]string) error {
			if *spawn {
				return client.Spawn(home(), *except)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			s := daemon.New(home())
			s.Browser = daemonBrowser(*reopen, os.Getenv, runtime.GOOS)
			s.ReopenExcept = *except
			return s.Serve(ctx)
		},
	}
}

func daemonBrowser(reopen bool, getenv func(string) string, goos string) func(url string) error {
	if !reopen {
		return nil
	}
	for _, v := range []string{"KAPI_NO_BROWSER", "SSH_CONNECTION", "SSH_TTY", "CI"} {
		if getenv(v) != "" {
			return nil
		}
	}
	if goos == "linux" && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return nil
	}
	return func(url string) error { return openBrowser(url) }
}

func attachCmd(c api, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	after := fs.String("after", "", "Element id to put the image after; the image goes after its top-level block, or at the end when left out")
	reader := fs.String("reader", "agent", "Reader name to add the image as")
	return &cli.Command{
		Name:       "attach",
		Short:      "Add an image to a page as a block",
		Long:       "Copy a PNG, JPEG, GIF or WebP image of up to 10 MB into the folder next to the page (plan.files/ for plan.html) and add it to the page as a figure. Prints the new element's id.",
		Flags:      fs,
		FlagsFirst: true,
		Args:       []cli.ArgDef{pageArg, {Name: "image", Required: true, Desc: "Image file to add"}},
		Examples: []cli.Example{
			{Command: "kapi attach --after k3f9a2bq plan.html chart.png", Description: "Add chart.png after one element"},
		},
		Run: func(args []string) error {
			pg, err := pagePath(args, 2, "attach")
			if err != nil {
				return err
			}
			if err := eventlog.CheckReader(*reader); err != nil {
				return err
			}
			if ext := strings.ToLower(filepath.Ext(pg)); ext != ".html" && ext != ".htm" {
				return fmt.Errorf("%s is not an HTML page", pg)
			}
			if _, err := os.Stat(pg); err != nil {
				return err
			}
			data, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			dir := pkgpage.FilesDir(pg)
			_, existed := os.Stat(dir)
			f, err := attach.Save(dir, data)
			if err != nil {
				return fmt.Errorf("%s: %w", args[1], err)
			}
			resp, err := c.Do(&daemon.Request{Op: "image", Page: pg, Author: *reader, Target: *after, Text: f.Name})
			if err != nil {
				if !referenced(pg, f.Name) {
					os.Remove(filepath.Join(dir, f.Name))
					if existed != nil {
						os.Remove(dir)
					}
				}
				return err
			}
			_, err = fmt.Fprintf(out, "added #%s (%s)\n", resp.Event.Target, resp.Event.Text)
			return err
		},
	}
}

func referenced(page, name string) bool {
	src, err := os.ReadFile(page)
	return err != nil || bytes.Contains(src, []byte(name))
}

func newPublishCmd(out io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "publish",
		Short: "Publish a read-only snapshot of a page to your hub and print its URL",
		Long: "Render the page's current source into one self-contained HTML document, with its images embedded, upload it to your hub\n" +
			"as a new revision, and print the page's hub URL. The hub comes from $KAPI_HOME/hub.json\n" +
			`({"url":"https://...","token_file":"hub-token"}) or KAPI_HUB_URL and KAPI_HUB_TOKEN.` + "\n" +
			"See hub/README.md to deploy a hub.",
		Flags: flag.NewFlagSet("publish", flag.ContinueOnError),
		Args:  []cli.ArgDef{pageArg},
		Examples: []cli.Example{
			{Command: "kapi publish plan.html", Description: "Publish plan.html and print its URL"},
		},
		Run: func(args []string) error {
			page, err := pagePath(args, 1, "publish")
			if err != nil {
				return err
			}
			src, err := os.ReadFile(page)
			if err != nil {
				return err
			}
			c, err := publish.FromHome(home(), os.Getenv)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			url, err := c.Publish(ctx, publish.PageID(page), uint64(time.Now().UnixMilli()), filepath.Base(page), src, pkgpage.FilesDir(page))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, url)
			return err
		},
	}
}

var stdoutIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func newExportCmd(out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	md := fs.Bool("md", false, "Export as Markdown")
	dir := fs.String("out", ".", "Folder to write the file into, or - to print it")
	force := fs.Bool("force", false, "Replace a file of the same name")
	return &cli.Command{
		Name:  "export",
		Short: "Export a page as a Markdown file",
		Long: "Convert the page's source to Markdown and write <page>.md into the folder given by --out.\n" +
			"When the page shows its own images, write <page>.zip instead: the .md and an images folder it links to.",
		Flags: fs,
		Args:  []cli.ArgDef{pageArg},
		Examples: []cli.Example{
			{Command: "kapi export --md plan.html", Description: "Write plan.md, or plan.zip when it has images, here"},
			{Command: "kapi export --md --out - plan.html", Description: "Print the Markdown"},
		},
		Run: func(args []string) error {
			if !*md {
				return errors.New("pick a format: --md\nSee 'kapi export --help' for usage")
			}
			page, err := pagePath(args, 1, "export")
			if err != nil {
				return err
			}
			src, err := os.ReadFile(page)
			if err != nil {
				return err
			}
			f, err := export.Markdown(filepath.Base(page), src, pkgpage.FilesDir(page))
			if err != nil {
				return err
			}
			if *dir == "-" {
				if f.Type == "application/zip" && stdoutIsTerminal(out) {
					return fmt.Errorf("the page shows images, so its export is a zip; redirect it to a file or pass --out <folder>")
				}
				_, err = out.Write(f.Data)
				return err
			}
			path := filepath.Join(*dir, f.Name)
			if err := writeExport(path, f.Data, *force); err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, path)
			return err
		},
	}
}

var linkFile = os.Link

func writeExport(path string, data []byte, replace bool) error {
	tmp, err := createExport(filepath.Dir(path), data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if replace {
		return os.Rename(tmp, path)
	}
	err = linkFile(tmp, path)
	if err != nil && !errors.Is(err, os.ErrExist) {
		err = writeNew(path, data)
	}
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; pass --force to replace it", path)
	}
	return err
}

func createExport(dir string, data []byte) (string, error) {
	for {
		name := filepath.Join(dir, ".kapi-export-"+rand.Text())
		err := writeNew(name, data)
		if !errors.Is(err, os.ErrExist) {
			return name, err
		}
	}
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // the user names the folder
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

var openBrowser = func(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, url).Run() //nolint:gosec // the URL comes from the local daemon
}

func newOpenCmd(c api, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "Print the URL without opening a browser")
	return &cli.Command{
		Name:  "open",
		Short: "Open a page in the browser",
		Long:  "Print the page's local URL and open it in the default browser. The URL holds a token, so treat it like a password.",
		Flags: fs,
		Args:  []cli.ArgDef{pageArg},
		Examples: []cli.Example{
			{Command: "kapi open plan.html", Description: "Open plan.html in the browser"},
			{Command: "kapi open --print plan.html", Description: "Only print the URL"},
		},
		Run: func(args []string) error {
			page, err := pagePath(args, 1, "open")
			if err != nil {
				return err
			}
			resp, err := c.Do(&daemon.Request{Op: "open", Page: page})
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, resp.URL); err != nil {
				return err
			}
			if resp.Notice != "" {
				if _, err := fmt.Fprintln(out, resp.Notice); err != nil {
					return err
				}
			}
			if *printOnly {
				return nil
			}
			return openBrowser(resp.URL)
		},
	}
}

func completionCmd(root *cli.Command, out io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "completion",
		Short: "Print a shell completion script",
		Examples: []cli.Example{
			{Command: `kapi completion zsh > "${fpath[1]}/_kapi"`, Description: "zsh"},
			{Command: "kapi completion bash > $(brew --prefix)/etc/bash_completion.d/kapi", Description: "bash on macOS"},
		},
		SubCommands: []*cli.Command{
			{Name: "zsh", Short: "Print the zsh completion script", Run: func([]string) error { return cli.WriteZshCompletion(out, root) }},
			{Name: "bash", Short: "Print the bash completion script", Run: func([]string) error { return cli.WriteBashCompletion(out, root) }},
		},
	}
}

func homeView(c api, out io.Writer) error {
	resp, err := c.Do(&daemon.Request{Op: "pages"})
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("kapi: shared HTML pages for you and your agent\n")
	if len(resp.Pages) == 0 {
		b.WriteString("pages: none yet\n")
	} else {
		fmt.Fprintf(&b, "pages[%d]:\n", len(resp.Pages))
		for i := range resp.Pages {
			p := &resp.Pages[i]
			path := p.Path
			if strings.ContainsAny(path, "\n\r\t") {
				path = strconv.Quote(path)
			}
			if p.Error != "" {
				fmt.Fprintf(&b, "  %s: error: %s\n", path, p.Error)
				continue
			}
			readers := make([]string, 0, len(p.Readers))
			for _, r := range p.Readers {
				s := fmt.Sprintf("%s %d unread", r.Reader, r.Pending)
				if r.Attached {
					s += " (watching)"
				}
				readers = append(readers, s)
			}
			if p.Ended {
				path += " (ended)"
			}
			fmt.Fprintf(&b, "  %s: %s\n", path, strings.Join(readers, ", "))
		}
	}
	b.WriteString(`next:
  kapi wait <page>                   wait for the next event, print every unseen one and exit; run it in the background, again after each exit
  kapi say --seen <n> <page> <text>  reply; n is the number of the last event you read, so the page shows you read it
  kapi say --reply <n> <page> <text> answer note #n; the page marks it answered
  kapi seen <page> <n>               show you read every event through #n, without replying
  kapi pending <page>                list unseen events and mark them seen
  kapi --help                        every command
`)
	_, err = io.WriteString(out, b.String())
	return err
}

func setupCmd(in io.Reader, out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "Remove kapi's hooks and skill instead of installing them")
	project := fs.Bool("project", false, "Install the skill in the current project instead of globally")
	agents := fs.String("agents", "", "Comma-separated agents: claude,codex,pi,opencode,copilot (default: ask, with detected agents preselected)")

	hooksFS := flag.NewFlagSet("hooks", flag.ContinueOnError)
	removeHooks := hooksFS.Bool("remove", false, "Remove kapi's hooks instead of installing them")
	root := &cli.Command{
		Name:  "setup",
		Short: "Install or repair kapi discovery for your agent harnesses",
		Long: "Install kapi's embedded Agent Skill and startup hooks without Node or npx. Re-running repairs kapi's files;\n" +
			"--remove deletes only files and hook entries managed by kapi. Use --project to write portable project skills.",
		Flags: fs,
		Run: func([]string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			selected, err := chooseHarnesses(in, out, *agents, home)
			if err != nil {
				return err
			}
			var projectRoot string
			if *project {
				projectRoot, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			var results []setup.Result
			skillOptions := setup.SkillOptions{Home: home, CopilotHome: os.Getenv("COPILOT_HOME"), Project: projectRoot, Harnesses: selected}
			if *remove {
				results = append(results, setup.RemoveSkills(skillOptions)...)
			} else {
				results = append(results, setup.InstallSkills(skillOptions)...)
			}
			if !*project {
				hookOptions := setup.Options{Home: home, CopilotHome: os.Getenv("COPILOT_HOME"), Harnesses: setup.Select(selected)}
				if *remove {
					results = append(results, setup.Remove(hookOptions)...)
				} else if needsHooks(selected) {
					hookOptions.Command, err = hookCommand()
					if err != nil {
						return err
					}
					results = append(results, setup.Install(hookOptions)...)
				}
			}
			return printSetupResults(out, results, !*remove)
		},
	}
	root.SubCommands = []*cli.Command{{
		Name:  "hooks",
		Short: "Install or repair the legacy startup hooks in every supported harness",
		Flags: hooksFS,
		Run: func([]string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			o := setup.Options{Home: home, CopilotHome: os.Getenv("COPILOT_HOME")}
			var results []setup.Result
			if *removeHooks {
				results = setup.Remove(o)
			} else {
				if o.Command, err = hookCommand(); err != nil {
					return err
				}
				results = setup.Install(o)
			}
			return printSetupResults(out, results, !*removeHooks)
		},
	}}
	return root
}

func chooseHarnesses(in io.Reader, out io.Writer, raw, home string) ([]string, error) {
	parse := func(s string) ([]string, error) {
		var selected []string
		for _, name := range strings.Split(s, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if !slices.Contains(setup.Harnesses, name) {
				return nil, fmt.Errorf("unknown agent %q; choose from %s", name, strings.Join(setup.Harnesses, ","))
			}
			if !slices.Contains(selected, name) {
				selected = append(selected, name)
			}
		}
		if len(selected) == 0 {
			return nil, errors.New("choose at least one agent")
		}
		return selected, nil
	}
	if strings.TrimSpace(raw) != "" {
		return parse(raw)
	}
	detected := detectedHarnesses(home)
	fallback := detected
	if len(fallback) == 0 {
		fallback = []string{setup.Claude, setup.Codex, setup.Pi}
	}
	fmt.Fprintf(out, "Agents to configure [%s]: ", strings.Join(fallback, ","))
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if strings.TrimSpace(line) == "" {
		return fallback, nil
	}
	return parse(line)
}

func detectedHarnesses(home string) []string {
	checks := []struct {
		name string
		cmd  string
		dir  string
	}{
		{setup.Claude, "claude", filepath.Join(home, ".claude")},
		{setup.Codex, "codex", filepath.Join(home, ".codex")},
		{setup.Pi, "pi", filepath.Join(home, ".pi")},
		{setup.OpenCode, "opencode", filepath.Join(home, ".config", "opencode")},
		{setup.Copilot, "copilot", filepath.Join(home, ".copilot")},
	}
	var found []string
	for _, c := range checks {
		_, cmdErr := exec.LookPath(c.cmd)
		_, dirErr := os.Stat(c.dir)
		if cmdErr == nil || dirErr == nil {
			found = append(found, c.name)
		}
	}
	return found
}

func needsHooks(selected []string) bool {
	return slices.ContainsFunc(selected, func(name string) bool { return name != setup.Pi })
}

func printSetupResults(out io.Writer, results []setup.Result, restart bool) error {
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(out, "  %-18s error: %v\n", r.Harness, r.Err)
			continue
		}
		fmt.Fprintf(out, "  %-18s %-9s %s\n", r.Harness, r.Status, r.Path)
	}
	if restart {
		fmt.Fprintln(out, "Restart your agent sessions so kapi is discovered.")
	}
	if failed > 0 {
		return fmt.Errorf("%d harness setup item(s) failed; see the errors above", failed)
	}
	return nil
}

func hookContextCmd(c api, out io.Writer) *cli.Command {
	return &cli.Command{
		Name:   "hook-context",
		Short:  "Print the home view as JSON for hooks that need additionalContext",
		Hidden: true,
		Run: func([]string) error {
			var view strings.Builder
			text := "## kapi ambient context\n"
			if err := homeView(c, &view); err != nil {
				text += "error: kapi ambient context failed: " + err.Error()
			} else {
				text += strings.TrimSpace(view.String())
			}
			enc := json.NewEncoder(out)
			enc.SetEscapeHTML(false)
			return enc.Encode(map[string]string{"additionalContext": text})
		},
	}
}

func hookMessagesCmd(c api, in io.Reader, out io.Writer) *cli.Command {
	return &cli.Command{
		Name:   "hook-messages",
		Short:  "Hand a Claude Code hook the unseen events on pages under its directory",
		Hidden: true,
		Run: func([]string) error {
			if cc, ok := c.(client.Client); ok {
				cc.NoStart = true
				c = cc
			}
			if err := hookMessages(c, in, out); err != nil && !errors.Is(err, client.ErrNotRunning) {
				fmt.Fprintln(os.Stderr, "kapi hook-messages:", err)
			}
			return nil
		},
	}
}

func hookMessages(c api, in io.Reader, out io.Writer) error {
	var hook struct {
		Event   string `json:"hook_event_name"`
		Cwd     string `json:"cwd"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(in).Decode(&hook); err != nil {
		return fmt.Errorf("hook input: %w", err)
	}
	if !slices.Contains([]string{"Stop", "PostToolUse", "UserPromptSubmit"}, hook.Event) || !filepath.IsAbs(hook.Cwd) || hook.AgentID != "" {
		return nil
	}
	resp, err := c.Do(&daemon.Request{Op: "inbox", Page: hook.Cwd, Reader: "agent"})
	if err != nil {
		return err
	}
	for i := range resp.Pages {
		if p := &resp.Pages[i]; p.Error != "" {
			fmt.Fprintf(os.Stderr, "kapi hook-messages: %s: %s\n", p.Path, p.Error)
		}
	}
	text, handed := hookText(resp.Pages, hook.Event == "UserPromptSubmit")
	if text == "" || (hook.Event == "Stop" && (len(handed) == 0 || !wakes(resp.Pages))) {
		return nil
	}
	var reply any = map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": hook.Event, "additionalContext": text}}
	if hook.Event == "Stop" {
		reply = map[string]string{"decision": "block", "reason": text}
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(reply); err != nil {
		return err
	}
	for _, h := range handed {
		if _, err := c.Do(&daemon.Request{Op: "handover", Page: h.Path, Reader: "agent", Seq: h.Through, Cut: h.Cut}); err != nil {
			return err
		}
	}
	return nil
}

func wakes(pages []daemon.PageStatus) bool {
	for i := range pages {
		p := &pages[i]
		if !p.Ended && slices.ContainsFunc(p.Events, func(e eventlog.Event) bool { return e.Seq > p.Hooked && !e.Quiet() }) {
			return true
		}
	}
	return false
}

const (
	hookTextLimit = 9000
	tooLongLine   = hookTextLimit / 2
	shownOfCut    = 300
)

type hookHanded struct {
	Path    string
	Through uint64
	Cut     []uint64
}

func hookText(pages []daemon.PageStatus, remind bool) (string, []hookHanded) {
	const head = "kapi: new events on a shared page; the human sees each one shown whole as read by you. Act on them and reply with `kapi say <page> <text>`.\n"
	var b strings.Builder
	var handed []hookHanded
	room := func(page *strings.Builder) int { return hookTextLimit - len(head) - b.Len() - page.Len() - 200 }
	for i := range pages {
		p := &pages[i]
		if p.Error != "" || len(p.Events) == 0 {
			continue
		}
		pending := fmt.Sprintf("`kapi pending %s`", p.Path)
		var page strings.Builder
		page.WriteString(p.Path + ":\n")
		h := hookHanded{Path: p.Path}
		if remind {
			for _, c := range p.Cut {
				fmt.Fprintf(&page, "  #%d is still unread; run %s\n", c, pending)
			}
		}
		for i := range p.Events {
			e := &p.Events[i]
			if e.Seq <= p.Hooked {
				continue
			}
			line := "  " + eventLine(e) + "\n"
			isCut := len(line) > tooLongLine
			if isCut {
				line = cut(line, shownOfCut) + fmt.Sprintf("… (too long to show here; run %s to read #%d)\n", pending, e.Seq)
			}
			if len(line) > room(&page) {
				fmt.Fprintf(&page, "  more unseen events here; run %s to read them\n", pending)
				break
			}
			page.WriteString(line)
			h.Through = e.Seq
			if isCut {
				h.Cut = append(h.Cut, e.Seq)
			}
		}
		if page.Len() > len(p.Path)+2 {
			b.WriteString(page.String())
		}
		if h.Through > 0 {
			handed = append(handed, h)
		}
	}
	if b.Len() == 0 {
		return "", nil
	}
	return head + strings.TrimSuffix(b.String(), "\n"), handed
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:max(n, 0)]
}

func hookCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolvedExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(resolvedExe, filepath.Clean(os.TempDir())) || strings.Contains(resolvedExe, string(filepath.Separator)+"go-build") {
		return "", fmt.Errorf("kapi is running from a temporary build (%s); install it first with `go install github.com/xaaha/kapi/cmd/kapi@latest`", resolvedExe)
	}
	if onPath, err := exec.LookPath("kapi"); err == nil {
		if resolved, err := filepath.EvalSymlinks(onPath); err == nil && resolved == resolvedExe {
			return "kapi", nil
		}
	}
	return resolvedExe, nil
}

func pagePath(args []string, want int, command string) (string, error) {
	if len(args) < want {
		return "", fmt.Errorf("expected %d argument(s), got %d\nSee 'kapi %s --help' for usage", want, len(args), command)
	}
	return filepath.Abs(args[0])
}

func eventLine(e *eventlog.Event) string {
	if e.Kind == eventlog.KindEnd {
		return eventlog.Printable(fmt.Sprintf("#%d %s ended the session: stop working on this page and do not run kapi wait or kapi watch on it again; a new message from the human starts it again", e.Seq, e.Author))
	}
	if e.Kind == eventlog.KindResolve {
		return eventlog.Printable(fmt.Sprintf("#%d %s resolved note %s", e.Seq, e.Author, seqList(e.Re, "#", ", ")))
	}
	if e.Kind == eventlog.KindDeleteNotes {
		return eventlog.Printable(fmt.Sprintf("#%d %s deleted note %s", e.Seq, e.Author, seqList(e.Re, "#", ", ")))
	}
	kind := e.Kind
	if len(e.Re) > 0 {
		kind = "reply to note " + seqList(e.Re, "#", ", ")
	}
	target := ""
	if e.Target != "" {
		target = " on #" + e.Target
	}
	if e.Part != "" {
		target += " (" + e.Part + ")"
	}
	if e.Quote != "" {
		target += fmt.Sprintf(" %q", e.Quote)
	}
	body := e.Text
	if len(e.Files) > 0 {
		body = strings.TrimSpace(body)
	}
	for _, f := range e.Files {
		body = strings.TrimPrefix(body+" [image "+f+"]", " ")
	}
	line := fmt.Sprintf("#%d %s %s%s: %s", e.Seq, e.Author, kind, target, body)
	if e.Kind == eventlog.KindNote && e.Author == "human" {
		answer := []uint64{e.Seq}
		if len(e.Re) > 0 {
			answer = e.Re
		}
		line += " (answer with kapi say --reply " + seqList(answer, "", ",") + ")"
	}
	return eventlog.Printable(line)
}

func seqList(seqs []uint64, prefix, sep string) string {
	out := make([]string, len(seqs))
	for i, n := range seqs {
		out[i] = prefix + strconv.FormatUint(n, 10)
	}
	return strings.Join(out, sep)
}
