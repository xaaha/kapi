package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xaaha/cli"

	"github.com/xaaha/kapi/internal/client"
	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
	"github.com/xaaha/kapi/internal/setup"
)

type api interface {
	Do(req *daemon.Request) (daemon.Response, error)
	Watch(page, reader string, fn func(daemon.Response) error) error
}

func main() {
	root := newRoot(client.Client{Home: home()}, os.Stdout, time.Sleep)
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

func newRoot(c api, out io.Writer, sleep func(time.Duration)) *cli.Command {
	root := &cli.Command{
		Name:   "kapi",
		Long:   "kapi: shared HTML pages for you and your agent.",
		Flags:  flag.NewFlagSet("kapi", flag.ContinueOnError),
		Config: &cli.Config{Out: out},
		Run:    func([]string) error { return homeView(c, out) },
		SubCommands: []*cli.Command{
			watchCmd(c, out, sleep),
			messageCmd(c, out, "say", "agent", "Reply on a page as the agent"),
			messageCmd(c, out, "send", "human", "Send a message on a page as the human"),
			pendingCmd(c, out),
			statusCmd(c, out),
			daemonCmd(),
			newPresetCmd(out),
		},
	}
	root.SubCommands = append(root.SubCommands, completionCmd(root, out), setupCmd(out), hookContextCmd(c, out))
	return root
}

func watchCmd(c api, out io.Writer, sleep func(time.Duration)) *cli.Command {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	reader := fs.String("reader", "agent", "Reader name; each reader keeps its own position")
	return &cli.Command{
		Name:  "watch",
		Short: "Stream a page's events, one line each",
		Long:  "Stream every unread event on a page, one line each, and keep streaming.\nReconnects if the daemon restarts. Each line is acknowledged after it is printed.",
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
			return watch(c, page, *reader, out, sleep)
		},
	}
}

func watch(c api, page, reader string, out io.Writer, sleep func(time.Duration)) error {
	for {
		err := c.Watch(page, reader, func(r daemon.Response) error {
			_, err := fmt.Fprintln(out, format(r.Event))
			return err
		})
		if !errors.Is(err, client.ErrConnectionLost) {
			return err
		}
		fmt.Fprintln(os.Stderr, "kapi: daemon connection lost, reconnecting")
		sleep(500 * time.Millisecond)
	}
}

func messageCmd(c api, out io.Writer, name, author, short string) *cli.Command {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	on := fs.String("on", "", "Element id the message is about")
	return &cli.Command{
		Name:       name,
		Short:      short,
		Long:       short + ". Prints the event number. Flags go before the page; every word after it is message text.",
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
			resp, err := c.Do(&daemon.Request{Op: "append", Page: page, Author: author, Kind: "message", Target: *on, Text: strings.Join(args[1:], " ")})
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
		Short: "List unread events without consuming them",
		Flags: fs,
		Args:  []cli.ArgDef{pageArg},
		Run: func(args []string) error {
			page, err := pagePath(args, 1, "pending")
			if err != nil {
				return err
			}
			resp, err := c.Do(&daemon.Request{Op: "pending", Page: page, Reader: *reader})
			if err != nil {
				return err
			}
			for i := range resp.Events {
				if _, err := fmt.Fprintln(out, format(&resp.Events[i])); err != nil {
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
	return &cli.Command{
		Name:  "daemon",
		Short: "Run the daemon in the foreground",
		Flags: flag.NewFlagSet("daemon", flag.ContinueOnError),
		Long:  "Run the daemon in the foreground. Other commands start it on demand, so this is only for debugging.",
		Run: func([]string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return daemon.New(home()).Serve(ctx)
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
		for _, p := range resp.Pages {
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
			fmt.Fprintf(&b, "  %s: %s\n", path, strings.Join(readers, ", "))
		}
	}
	b.WriteString(`next:
  kapi watch <page>       stream a page's events, one line each; run it where each line wakes you
  kapi say <page> <text>  reply on a page
  kapi pending <page>     list unread events without consuming them
  kapi --help             every command
`)
	_, err = io.WriteString(out, b.String())
	return err
}

func setupCmd(out io.Writer) *cli.Command {
	fs := flag.NewFlagSet("hooks", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "Remove kapi's hooks instead of installing them")
	return &cli.Command{
		Name:  "setup",
		Short: "Connect kapi to your agent harnesses",
		SubCommands: []*cli.Command{{
			Name:  "hooks",
			Short: "Install or repair the SessionStart hook in Claude Code, Codex, OpenCode and Copilot CLI",
			Long: "Install or repair a SessionStart hook that runs `kapi` so every new agent session sees your pages\n" +
				"and their unread messages. Covers Claude Code, Codex, OpenCode and GitHub Copilot CLI. Re-running\n" +
				"repairs the hook; --remove takes it out again. Restart your agent sessions afterwards.",
			Flags: fs,
			Run: func([]string) error {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				o := setup.Options{Home: home, CopilotHome: os.Getenv("COPILOT_HOME")}
				var results []setup.Result
				if *remove {
					results = setup.Remove(o)
				} else {
					if o.Command, err = hookCommand(); err != nil {
						return err
					}
					results = setup.Install(o)
				}
				failed := 0
				for _, r := range results {
					if r.Err != nil {
						failed++
						fmt.Fprintf(out, "  %-15s error: %v\n", r.Harness, r.Err)
						continue
					}
					fmt.Fprintf(out, "  %-15s %-9s %s\n", r.Harness, r.Status, r.Path)
				}
				if !*remove {
					fmt.Fprintln(out, "Restart your agent sessions so the hook takes effect.")
				}
				if failed > 0 {
					return fmt.Errorf("%d harness(es) failed; see the errors above", failed)
				}
				return nil
			},
		}},
	}
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

func format(e *eventlog.Event) string {
	target := ""
	if e.Target != "" {
		target = " on #" + e.Target
	}
	return fmt.Sprintf("#%d %s %s%s: %s", e.Seq, e.Author, e.Kind, target, strings.ReplaceAll(e.Text, "\n", `\n`))
}
