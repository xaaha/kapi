package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/xaaha/kapi/internal/daemon"
)

func stubBrowser(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	prev := openBrowser
	openBrowser = func(url string) error {
		opened = append(opened, url)
		return err
	}
	t.Cleanup(func() { openBrowser = prev })
	return &opened
}

func TestOpenPrintsTheURLAndOpensIt(t *testing.T) {
	opened := stubBrowser(t, nil)
	const url = "http://127.0.0.1:5000/tok/p/0123456789abcdef/"
	f := &fakeAPI{resp: daemon.Response{URL: url}}
	out, err := run(t, f, "open", "plan.html")
	if err != nil {
		t.Fatal(err)
	}
	if out != url+"\n" {
		t.Errorf("output = %q", out)
	}
	if want := (daemon.Request{Op: "open", Page: abs(t, "plan.html")}); len(f.requests) != 1 || !reflect.DeepEqual(f.requests[0], want) {
		t.Errorf("requests = %+v, want %+v", f.requests, want)
	}
	if len(*opened) != 1 || (*opened)[0] != url {
		t.Errorf("opened = %v", *opened)
	}
}

func TestOpenWithPrintOnlyPrints(t *testing.T) {
	opened := stubBrowser(t, nil)
	f := &fakeAPI{resp: daemon.Response{URL: "http://127.0.0.1:5000/tok/p/x/"}}
	if _, err := run(t, f, "open", "--print", "plan.html"); err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 0 {
		t.Errorf("--print opened %v", *opened)
	}
}

func TestOpenReportsABrowserFailure(t *testing.T) {
	failed := errors.New("no browser")
	stubBrowser(t, failed)
	f := &fakeAPI{resp: daemon.Response{URL: "http://127.0.0.1:5000/tok/p/x/"}}
	if _, err := run(t, f, "open", "plan.html"); !errors.Is(err, failed) {
		t.Errorf("err = %v, want the browser error", err)
	}
}

func TestOpenPrintsAConversionNotice(t *testing.T) {
	stubBrowser(t, nil)
	notice := "split 1 block(s) so each has its own id; raw input saved to /home/.kapi/pages/x/raw/1.html"
	f := &fakeAPI{resp: daemon.Response{URL: "http://127.0.0.1:5000/tok/p/x/", Notice: notice}}
	out, err := run(t, f, "open", "plan.html")
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://127.0.0.1:5000/tok/p/x/\n" + notice + "\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
