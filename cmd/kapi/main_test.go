package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/client"
	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/eventlog"
)

type fakeAPI struct {
	requests []daemon.Request
	resp     daemon.Response
	watches  []func(fn func(daemon.Response) error) error
	watched  []string
}

func (f *fakeAPI) Do(req *daemon.Request) (daemon.Response, error) {
	f.requests = append(f.requests, *req)
	return f.resp, nil
}

func (f *fakeAPI) Watch(page, reader string, fn func(daemon.Response) error) error {
	f.watched = append(f.watched, page+"|"+reader)
	next := f.watches[0]
	f.watches = f.watches[1:]
	return next(fn)
}

func run(t *testing.T, f *fakeAPI, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := newRoot(f, &out, func(time.Duration) {}).Execute(args)
	return out.String(), err
}

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestD10SendAndSayBuildMessages(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantAuthor string
		wantTarget string
	}{
		{"send with flag first", []string{"send", "--on", "cutover", "plan.html", "is", "this", "true?"}, "human", "cutover"},
		{"say", []string{"say", "plan.html", "is", "this", "true?"}, "agent", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeAPI{resp: daemon.Response{Event: &eventlog.Event{Seq: 7}}}
			out, err := run(t, f, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if out != "sent #7\n" {
				t.Errorf("output = %q", out)
			}
			want := daemon.Request{Op: "append", Page: abs(t, "plan.html"), Author: tc.wantAuthor, Kind: "message", Target: tc.wantTarget, Text: "is this true?"}
			if len(f.requests) != 1 || f.requests[0] != want {
				t.Errorf("requests = %+v, want %+v", f.requests, want)
			}
		})
	}
}

func TestD10MissingArgumentsPointToHelp(t *testing.T) {
	for _, args := range [][]string{{"say", "plan.html"}, {"watch"}, {"pending"}, {"status"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := &fakeAPI{}
			_, err := run(t, f, args...)
			if err == nil || !strings.Contains(err.Error(), "See 'kapi "+args[0]+" --help'") {
				t.Fatalf("error = %v", err)
			}
			if len(f.requests) != 0 {
				t.Errorf("sent %d requests despite missing arguments", len(f.requests))
			}
		})
	}
}

func TestD10WatchReconnectsAfterLostConnection(t *testing.T) {
	stop := errors.New("stop")
	f := &fakeAPI{watches: []func(func(daemon.Response) error) error{
		func(func(daemon.Response) error) error { return fmt.Errorf("%w: EOF", client.ErrConnectionLost) },
		func(fn func(daemon.Response) error) error {
			if err := fn(daemon.Response{Event: &eventlog.Event{Seq: 3, Author: "human", Kind: "message", Target: "x", Text: "a\nb"}}); err != nil {
				return err
			}
			return stop
		},
	}}
	out, err := run(t, f, "watch", "--reader", "codex", "plan.html")
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the second watch's error", err)
	}
	if out != "#3 human message on #x: a\\nb\n" {
		t.Errorf("output = %q", out)
	}
	want := abs(t, "plan.html") + "|codex"
	if len(f.watched) != 2 || f.watched[0] != want || f.watched[1] != want {
		t.Errorf("watched = %v, want two watches of %s", f.watched, want)
	}
}

func TestD10PendingAndStatus(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{
		Events:  []eventlog.Event{{Seq: 1, Author: "human", Kind: "message", Text: "hi"}, {Seq: 2, Author: "agent", Kind: "message", Text: "yo"}},
		Readers: []daemon.ReaderStatus{{Reader: "agent", Pending: 1}, {Reader: "codex", Attached: true, Since: time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)}},
	}}
	out, err := run(t, f, "pending", "plan.html", "--reader", "codex")
	if err != nil || out != "#1 human message: hi\n#2 agent message: yo\n" {
		t.Fatalf("pending = %q, %v", out, err)
	}
	if f.requests[0].Reader != "codex" || f.requests[0].Op != "pending" {
		t.Errorf("pending request = %+v", f.requests[0])
	}
	out, err = run(t, f, "status", "plan.html")
	if err != nil || out != "agent: not attached, 1 unread\ncodex: attached since 03:04:05, 0 unread\n" {
		t.Fatalf("status = %q, %v", out, err)
	}
}

func TestD10HelpAndCompletion(t *testing.T) {
	out, err := run(t, &fakeAPI{}, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"watch", "say", "send", "pending", "status", "daemon", "completion", "Use `kapi <command> --help`"} {
		if !strings.Contains(out, name) {
			t.Errorf("root help missing %q:\n%s", name, out)
		}
	}
	out, err = run(t, &fakeAPI{}, "completion", "bash")
	if err != nil || !strings.Contains(out, "complete -F _kapi kapi") {
		t.Fatalf("bash completion = %v:\n%s", err, out)
	}
	out, err = run(t, &fakeAPI{}, "completion", "zsh")
	if err != nil || !strings.HasPrefix(out, "#compdef kapi\n") {
		t.Fatalf("zsh completion = %v:\n%s", err, out)
	}
	if _, err := run(t, &fakeAPI{}, "bogus"); err == nil || !strings.Contains(err.Error(), `unknown command "bogus" for kapi`) {
		t.Errorf("unknown command error = %v", err)
	}
}

func TestReviewF1MessageWordsStartingWithADashAreText(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Event: &eventlog.Event{Seq: 1}}}
	if _, err := run(t, f, "say", "--on", "cutover", "plan.html", "-", "item", "-5", "--on", "x"); err != nil {
		t.Fatal(err)
	}
	if got := f.requests[0]; got.Text != "- item -5 --on x" || got.Target != "cutover" {
		t.Errorf("request = %+v", got)
	}
}

func TestReviewF2F3UnknownFlagsAreErrors(t *testing.T) {
	for _, args := range [][]string{{"status", "--reader", "agent", "plan.html"}, {"--bogus"}} {
		f := &fakeAPI{}
		if _, err := run(t, f, args...); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Errorf("%v: error = %v", args, err)
		}
		if len(f.requests) != 0 {
			t.Errorf("%v: sent %d requests", args, len(f.requests))
		}
	}
}

func TestReviewF2DaemonRejectsUnknownFlags(t *testing.T) {
	home, err := os.MkdirTemp("", "kapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("KAPI_HOME", home)
	done := make(chan error, 1)
	go func() {
		_, err := run(t, &fakeAPI{}, "daemon", "--bogus")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Errorf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kapi daemon --bogus started the daemon instead of failing")
	}
}

func TestHomeViewListsPagesAndNextSteps(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{
		{Path: "/w/plan.html", Readers: []daemon.ReaderStatus{{Reader: "agent", Pending: 2, Attached: true}, {Reader: "codex"}}},
		{Path: "/w/notes.html", Readers: []daemon.ReaderStatus{{Reader: "agent"}}},
	}}}
	out, err := run(t, f)
	if err != nil {
		t.Fatal(err)
	}
	want := `kapi: shared HTML pages for you and your agent
pages[2]:
  /w/plan.html: agent 2 unread (watching), codex 0 unread
  /w/notes.html: agent 0 unread
next:
  kapi watch <page>       stream a page's events, one line each; run it where each line wakes you
  kapi say <page> <text>  reply on a page
  kapi pending <page>     list unread events without consuming them
  kapi --help             every command
`
	if out != want {
		t.Errorf("home view:\n%s\nwant:\n%s", out, want)
	}
	if len(f.requests) != 1 || f.requests[0].Op != "pages" {
		t.Errorf("requests = %+v", f.requests)
	}
}

func TestHomeViewWithNoPages(t *testing.T) {
	out, err := run(t, &fakeAPI{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pages: none yet\n") || !strings.Contains(out, "kapi watch <page>") {
		t.Errorf("home view:\n%s", out)
	}
}
