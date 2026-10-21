package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/xaaha/kapi/internal/client"
	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
)

const usage = `kapi: shared HTML pages for you and your agent

  kapi watch   [--reader agent] <page>        stream events, one line each
  kapi say     <page> <text>                  reply as the agent
  kapi send    [--on id] <page> <text>        send as the human
  kapi pending [--reader agent] <page>        list unread events without consuming them
  kapi status  <page>                         show who is attached
  kapi daemon                                 run the daemon in the foreground
`

func main() {
	if err := run(os.Args[1:]); err != nil {
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

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, args := args[0], args[1:]
	c := client.Client{Home: home()}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	reader := fs.String("reader", "agent", "reader name")
	on := fs.String("on", "", "target element id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()

	switch cmd {
	case "daemon":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return daemon.New(home()).Serve(ctx)
	case "watch":
		page, err := pageArg(rest, 1)
		if err != nil {
			return err
		}
		for {
			err := c.Watch(page, *reader, func(r daemon.Response) error {
				_, err := fmt.Println(format(*r.Event))
				return err
			})
			if !errors.Is(err, client.ErrConnectionLost) {
				return err
			}
			fmt.Fprintln(os.Stderr, "kapi: daemon connection lost, reconnecting")
			time.Sleep(500 * time.Millisecond)
		}
	case "say", "send":
		page, err := pageArg(rest, 2)
		if err != nil {
			return err
		}
		author := "agent"
		if cmd == "send" {
			author = "human"
		}
		resp, err := c.Do(daemon.Request{Op: "append", Page: page, Author: author, Kind: "message", Target: *on, Text: strings.Join(rest[1:], " ")})
		if err != nil {
			return err
		}
		fmt.Printf("sent #%d\n", resp.Event.Seq)
		return nil
	case "pending":
		page, err := pageArg(rest, 1)
		if err != nil {
			return err
		}
		resp, err := c.Do(daemon.Request{Op: "pending", Page: page, Reader: *reader})
		if err != nil {
			return err
		}
		for _, e := range resp.Events {
			fmt.Println(format(e))
		}
		return nil
	case "status":
		page, err := pageArg(rest, 1)
		if err != nil {
			return err
		}
		resp, err := c.Do(daemon.Request{Op: "status", Page: page})
		if err != nil {
			return err
		}
		for _, r := range resp.Readers {
			state := "not attached"
			if r.Attached {
				state = "attached since " + r.Since.Local().Format("15:04:05")
			}
			fmt.Printf("%s: %s, %d unread\n", r.Reader, state, r.Pending)
		}
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func pageArg(rest []string, min int) (string, error) {
	if len(rest) < min {
		return "", fmt.Errorf("expected %d argument(s), got %d\n\n%s", min, len(rest), usage)
	}
	return filepath.Abs(rest[0])
}

func format(e eventlog.Event) string {
	target := ""
	if e.Target != "" {
		target = " on #" + e.Target
	}
	return fmt.Sprintf("#%d %s %s%s: %s", e.Seq, e.Author, e.Kind, target, strings.ReplaceAll(e.Text, "\n", `\n`))
}
