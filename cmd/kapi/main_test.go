package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	stdin    string
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
	err := newRoot(f, strings.NewReader(f.stdin), &out, func(time.Duration) {}).Execute(args)
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
	if out != "#3 human message on #x: a b\n" {
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
	if _, err := run(t, f, "say", "--on", "cutover", "plan.html", "-on", "the", "other", "hand", "-", "item", "-5", "--x"); err != nil {
		t.Fatal(err)
	}
	if got := f.requests[0]; got.Text != "-on the other hand - item -5 --x" || got.Target != "cutover" {
		t.Errorf("request = %+v", got)
	}
}

func TestACommandFlagAnywhereAfterThePageIsRefusedInsteadOfSentAsText(t *testing.T) {
	for _, args := range [][]string{
		{"say", "plan.html", "--seen", "3", "x"},
		{"say", "plan.html", "hello", "--seen", "1"},
		{"say", "plan.html", "x", "--on=cutover"},
		{"send", "plan.html", "--on", "cutover", "is this true?"},
	} {
		f := &fakeAPI{}
		_, err := run(t, f, args...)
		if err == nil || !strings.Contains(err.Error(), "put flags before the page") {
			t.Errorf("%v: error = %v", args, err)
		}
		if len(f.requests) != 0 {
			t.Errorf("%v: sent %+v", args, f.requests)
		}
	}
	f := &fakeAPI{resp: daemon.Response{Event: &eventlog.Event{Seq: 1}}}
	if _, err := run(t, f, "send", "plan.html", "--verbose", "is", "text"); err != nil || f.requests[0].Text != "--verbose is text" {
		t.Errorf("a word that is not a flag of send = %+v, %v; want it sent as text", f.requests, err)
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
  kapi wait <page>                   wait for the next event, print every unseen one and exit; run it in the background, again after each exit
  kapi say --seen <n> <page> <text>  reply; n is the number of the last event you read, so the page shows you read it
  kapi seen <page> <n>               show you read every event through #n, without replying
  kapi pending <page>                list unseen events and mark them seen
  kapi --help                        every command
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
	if !strings.Contains(out, "pages: none yet\n") || !strings.Contains(out, "kapi wait <page>") {
		t.Errorf("home view:\n%s", out)
	}
}

func TestHomeViewShowsBrokenPagesAndQuotesOddPaths(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{
		{Path: "/w/bad.html", Error: "events.ndjson line 1: invalid character"},
		{Path: "/w/two\nlines.html", Readers: []daemon.ReaderStatus{{Reader: "agent", Pending: 1}}},
	}}}
	out, err := run(t, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  /w/bad.html: error: events.ndjson line 1: invalid character\n",
		"  \"/w/two\\nlines.html\": agent 1 unread\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("home view missing %q:\n%s", want, out)
		}
	}
}

func TestHookContextWrapsTheHomeViewAsJSON(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Readers: []daemon.ReaderStatus{{Reader: "agent", Pending: 1}}}}}}
	out, err := run(t, f, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		AdditionalContext string `json:"additionalContext"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(got.AdditionalContext, "## kapi ambient context\nkapi: shared HTML pages") || !strings.Contains(got.AdditionalContext, "/w/plan.html: agent 1 unread") {
		t.Errorf("additionalContext = %q", got.AdditionalContext)
	}
}

func TestSetupHooksRefusesATemporaryBuildAndRemovesCleanly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COPILOT_HOME", "")
	if _, err := run(t, &fakeAPI{}, "setup", "hooks"); err == nil || !strings.Contains(err.Error(), "temporary build") {
		t.Errorf("install from a test binary: error = %v", err)
	}
	out, err := run(t, &fakeAPI{}, "setup", "hooks", "--remove")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Claude Code", "Codex", "Copilot CLI", "OpenCode"} {
		if !strings.Contains(out, h) || !strings.Contains(out, "absent") {
			t.Errorf("remove output missing %s absent:\n%s", h, out)
		}
	}
}

func TestReviewH5SetupHooksFailsWhenAHarnessFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COPILOT_HOME", "")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, &fakeAPI{}, "setup", "hooks", "--remove")
	if err == nil || !strings.Contains(err.Error(), "1 harness") {
		t.Errorf("error = %v", err)
	}
	if !strings.Contains(out, "Claude Code") || !strings.Contains(out, "Copilot CLI") {
		t.Errorf("every harness should still be reported:\n%s", out)
	}
}

func TestEventLinesCarryNoTerminalControls(t *testing.T) {
	e := &eventlog.Event{Seq: 4, Author: "human\x1b[31m", Kind: "revision", Target: "a\x1b[2J", Text: "edited \"\x1b]0;owned\x07\x1b[2J\u202ex\""}
	if got, want := eventLine(e), `#4 human[31m revision on #a[2J: edited "]0;owned[2Jx"`; got != want {
		t.Errorf("eventLine = %q, want %q", got, want)
	}
}

func TestWaitPrintsEveryUnseenEventThenQueuesThem(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Events: []eventlog.Event{{Seq: 2, Author: "human", Kind: "message", Text: "one"}, {Seq: 5, Author: "human", Kind: "answer", Text: "two"}}}}
	out, err := run(t, f, "wait", "--reader", "codex", "plan.html")
	if err != nil || out != "#2 human message: one\n#5 human answer: two\n" {
		t.Fatalf("wait = %q, %v", out, err)
	}
	page := abs(t, "plan.html")
	want := []daemon.Request{{Op: "wait", Page: page, Reader: "codex"}, {Op: "queue", Page: page, Reader: "codex", Seq: 5}}
	if len(f.requests) != 2 || f.requests[0] != want[0] || f.requests[1] != want[1] {
		t.Errorf("requests = %+v, want %+v", f.requests, want)
	}
}

func TestSeenReportsHowFarItMarked(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Event: &eventlog.Event{Seq: 9}}}
	out, err := run(t, f, "seen", "plan.html", "#9")
	if err != nil || out != "seen through #9\n" {
		t.Fatalf("seen = %q, %v", out, err)
	}
	if f.requests[0].Op != "seen" || f.requests[0].Reader != "agent" || f.requests[0].Seq != 9 {
		t.Errorf("request = %+v", f.requests[0])
	}
}

func TestSayWithSeenNamesTheLastEventRead(t *testing.T) {
	f := &fakeAPI{resp: daemon.Response{Event: &eventlog.Event{Seq: 7}}}
	if _, err := run(t, f, "say", "--seen", "#5", "plan.html", "ok"); err != nil {
		t.Fatal(err)
	}
	if f.requests[0].Seq != 5 || f.requests[0].Author != "agent" {
		t.Errorf("request = %+v, want seen #5", f.requests[0])
	}
	for _, bad := range []string{"0", "x", "#"} {
		if _, err := run(t, &fakeAPI{}, "say", "--seen", bad, "plan.html", "ok"); err == nil {
			t.Errorf("--seen %q accepted", bad)
		}
	}
	if _, err := run(t, &fakeAPI{}, "send", "--seen", "3", "plan.html", "hi"); err == nil {
		t.Error("send accepted --seen, which only a reader can give")
	}
}

func TestHookMessagesAnswersEachClaudeCodeEvent(t *testing.T) {
	inbox := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/plan.html", Events: []eventlog.Event{{Seq: 4, Author: "human", Kind: "message", Text: "<ship it>"}}}}}
	cases := []struct {
		stdin string
		want  string
	}{
		{`{"hook_event_name":"Stop","cwd":"/w"}`, `{"decision":"block","reason":"kapi: `},
		{`{"hook_event_name":"PostToolUse","cwd":"/w"}`, `{"hookSpecificOutput":{"additionalContext":"kapi: `},
		{`{"hook_event_name":"UserPromptSubmit","cwd":"/w"}`, `{"hookSpecificOutput":{"additionalContext":"kapi: `},
	}
	for _, tc := range cases {
		f := &fakeAPI{resp: inbox, stdin: tc.stdin}
		out, err := run(t, f, "hook-messages")
		if err != nil {
			t.Fatalf("%s: %v", tc.stdin, err)
		}
		if !strings.HasPrefix(out, tc.want) || !strings.Contains(out, `/w/plan.html:\n  #4 human message: <ship it>`) || !strings.Contains(out, "kapi say <page> <text>") {
			t.Errorf("%s: output = %s", tc.stdin, out)
		}
		want := []daemon.Request{
			{Op: "inbox", Page: "/w", Reader: "agent"},
			{Op: "queue", Page: "/w/plan.html", Reader: "agent", Seq: 4},
			{Op: "seen", Page: "/w/plan.html", Reader: "agent", Seq: 4},
		}
		if !slices.Equal(f.requests, want) {
			t.Errorf("%s: requests = %+v, want %+v", tc.stdin, f.requests, want)
		}
	}
	for _, stdin := range []string{`{"hook_event_name":"SessionStart","cwd":"/w"}`, `{"hook_event_name":"Stop","cwd":"rel"}`, `{"hook_event_name":"PostToolUse","cwd":"/w","agent_id":"a1","agent_type":"Explore"}`} {
		f := &fakeAPI{resp: inbox, stdin: stdin}
		if out, err := run(t, f, "hook-messages"); err != nil || out != "" || len(f.requests) != 0 {
			t.Errorf("%s: %q, %v, %d requests; want nothing", stdin, out, err, len(f.requests))
		}
	}
	f := &fakeAPI{stdin: `{"hook_event_name":"Stop","cwd":"/w"}`}
	if out, err := run(t, f, "hook-messages"); err != nil || out != "" {
		t.Errorf("nothing unseen: %q, %v; want no output so the agent may stop", out, err)
	}
	broken := daemon.Response{Pages: []daemon.PageStatus{{Path: "/w/bad.html", Error: "queued.json: bad"}}}
	for _, f := range []*fakeAPI{{stdin: "not json"}, {resp: broken, stdin: `{"hook_event_name":"Stop","cwd":"/w"}`}} {
		if out, err := run(t, f, "hook-messages"); err != nil || out != "" {
			t.Errorf("%s: %q, %v; want no output and a zero exit so the agent carries on", f.stdin, out, err)
		}
	}
}
