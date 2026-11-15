package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/daemon"
	"github.com/xaaha/kapi/internal/web/app"
)

const homeURL = "http://127.0.0.1:5000/tok/"

func TestHomePrintsTheNotebookURLAndOpensIt(t *testing.T) {
	t.Setenv("KAPI_NO_BROWSER", "")
	opened := stubBrowser(t, nil)
	f := &fakeAPI{resp: daemon.Response{URL: homeURL}}
	out, err := run(t, f, "home")
	if err != nil {
		t.Fatal(err)
	}
	if out != homeURL+"\n" {
		t.Errorf("output = %q", out)
	}
	if want := (daemon.Request{Op: "home"}); len(f.requests) != 1 || !reflect.DeepEqual(f.requests[0], want) {
		t.Errorf("requests = %+v, want %+v", f.requests, want)
	}
	if len(*opened) != 1 || (*opened)[0] != homeURL {
		t.Errorf("opened = %v", *opened)
	}
}

func TestHomeOnlyPrintsWithPrintOrKapiNoBrowser(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		args []string
	}{
		{"--print", "", []string{"home", "--print"}},
		{"KAPI_NO_BROWSER", "1", []string{"home"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KAPI_NO_BROWSER", tc.env)
			opened := stubBrowser(t, nil)
			out, err := run(t, &fakeAPI{resp: daemon.Response{URL: homeURL}}, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if out != homeURL+"\n" || len(*opened) != 0 {
				t.Errorf("output %q, opened %v; want only the URL printed", out, *opened)
			}
		})
	}
}

func TestHomeAndLsTakeNoArguments(t *testing.T) {
	stubBrowser(t, nil)
	for _, cmd := range []string{"home", "ls"} {
		f := &fakeAPI{}
		if _, err := run(t, f, cmd, "plan.html"); err == nil || len(f.requests) != 0 {
			t.Errorf("kapi %s plan.html: err %v, requests %+v; want refused before asking the daemon", cmd, err, f.requests)
		}
	}
}

func listed(at time.Time) []app.Page {
	return []app.Page{
		{ID: "1111111111111111", Title: "Rollout plan", Path: "/work/rollout.html", State: app.Live, Viewed: at.Add(-90 * time.Second), Touched: at.Add(-time.Hour), Summary: "not listed"},
		{ID: "2222222222222222", Title: "Notes\twith a tab", Path: "/work/notes.html", State: app.Ended, Viewed: at.Add(-3 * 24 * time.Hour), Touched: at.Add(-4 * 24 * time.Hour)},
		{ID: "3333333333333333", Title: "Fresh", Path: "/work/fresh.html", State: app.Idle, Viewed: at.Add(-10 * time.Second), Touched: at.Add(-10 * time.Second)},
	}
}

func fixNow(t *testing.T) time.Time {
	t.Helper()
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	prev := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = prev })
	return at
}

func TestLsPrintsColumnsInTheDaemonsOrder(t *testing.T) {
	at := fixNow(t)
	f := &fakeAPI{resp: daemon.Response{Index: listed(at)}}
	out, err := run(t, f, "ls")
	if err != nil {
		t.Fatal(err)
	}
	if want := (daemon.Request{Op: "index"}); len(f.requests) != 1 || !reflect.DeepEqual(f.requests[0], want) {
		t.Errorf("requests = %+v, want %+v", f.requests, want)
	}
	want := strings.Join([]string{
		`TITLE                STATE  VIEWED        PATH`,
		`Rollout plan         live   1 minute ago  /work/rollout.html`,
		`"Notes\twith a tab"  ended  3 days ago    /work/notes.html`,
		`Fresh                idle   just now      /work/fresh.html`,
	}, "\n") + "\n"
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestLsSaysSoWhenThereAreNoPages(t *testing.T) {
	out, err := run(t, &fakeAPI{}, "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No pages yet") {
		t.Errorf("output = %q", out)
	}
}

func TestLsJSONGivesAgentsIDsPathsStatesAndUTCTimes(t *testing.T) {
	at := fixNow(t)
	pages := listed(at)
	est := time.FixedZone("EST", -5*3600)
	pages[0].Viewed = pages[0].Viewed.In(est)
	out, err := run(t, &fakeAPI{resp: daemon.Response{Index: pages}}, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 3 || got[0]["id"] != "1111111111111111" || got[0]["title"] != "Rollout plan" || got[0]["path"] != "/work/rollout.html" || got[0]["state"] != "live" {
		t.Fatalf("got %v", got)
	}
	if got[0]["viewed"] != "2026-11-15T11:58:30Z" || got[0]["touched"] != "2026-11-15T11:00:00Z" {
		t.Errorf("times = %v and %v, want UTC", got[0]["viewed"], got[0]["touched"])
	}
	if _, ok := got[0]["summary"]; ok {
		t.Errorf("listed the summary: %v", got[0])
	}
	empty, err := run(t, &fakeAPI{}, "ls", "--json")
	if err != nil || strings.TrimSpace(empty) != "[]" {
		t.Errorf("no pages as JSON = %q, %v; want []", empty, err)
	}
}

func TestAgoRoundsDownToTheLargestUnit(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		back time.Duration
		want string
	}{
		{0, "just now"},
		{59 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{2 * time.Hour, "2 hours ago"},
		{8 * 24 * time.Hour, "1 week ago"},
		{65 * 24 * time.Hour, "2 months ago"},
		{800 * 24 * time.Hour, "2 years ago"},
		{-time.Hour, "just now"},
	} {
		if got := ago(at.Add(-tc.back), at); got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.back, got, tc.want)
		}
	}
}

func TestLsLeavesOutArchivedPagesUnlessAll(t *testing.T) {
	at := fixNow(t)
	pages := listed(at)
	pages[1].ArchivedAt = at.Add(-2 * 24 * time.Hour)
	pages[1].DeletesAt = at.Add(28 * 24 * time.Hour)
	pages[2].Saved = true
	ls := func(args ...string) string {
		t.Helper()
		out, err := run(t, &fakeAPI{resp: daemon.Response{Index: slices.Clone(pages)}}, append([]string{"ls"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := ls(); strings.Contains(out, "notes.html") || !strings.Contains(out, "rollout.html") || !strings.Contains(out, "fresh.html") {
		t.Errorf("kapi ls listed:\n%s\nwant every page but the archived one", out)
	}
	if out := ls("--all"); !strings.Contains(out, `"Notes\twith a tab"  ended, archived  3 days ago`) {
		t.Errorf("kapi ls --all printed:\n%s\nwant the archived page marked archived", out)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(ls("--json", "--all")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0]["saved"] != false || got[0]["archived"] != false || got[2]["saved"] != true {
		t.Fatalf("saved and archived flags: %v", got)
	}
	if got[1]["archived"] != true || got[1]["archivedAt"] != "2026-11-13T12:00:00Z" || got[1]["deletesAt"] != "2026-12-13T12:00:00Z" {
		t.Errorf("archived page as JSON: %v", got[1])
	}
	if _, ok := got[0]["archivedAt"]; ok {
		t.Errorf("a page that is not archived has archivedAt: %v", got[0])
	}
	out, err := run(t, &fakeAPI{resp: daemon.Response{Index: pages[1:2]}}, "ls")
	if err != nil || !strings.Contains(out, "kapi ls --all") {
		t.Errorf("kapi ls with only archived pages printed %q, %v", out, err)
	}
}
