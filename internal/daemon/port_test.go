package daemon

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixPort(t *testing.T, home string) string {
	t.Helper()
	port := freePort(t)
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetPort(home, n); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestAFixedPortKeepsItsTokenSoAnOpenTabResumesAfterARestart(t *testing.T) {
	home := shortHome(t)
	port := fixPort(t, home)
	path := writePage(t, "<p>x</p>\n")
	_, stop := servingAt(t, home)
	before := httpURL(t, home)
	pageURL := call(t, home, &Request{Op: "open", Page: path}).URL
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "before"})
	if id := firstEventID(t, pageURL, ""); id != "1" {
		t.Fatalf("first event id = %s", id)
	}
	stop()

	_, stop = servingAt(t, home)
	defer stop()
	if after := httpURL(t, home); after != before || !strings.Contains(after, "127.0.0.1:"+port+"/") {
		t.Fatalf("http.url after restart = %s, was %s; want port %s and the same token", after, before, port)
	}
	if fi, err := os.Stat(TokenPath(home)); err != nil || !modeIs(fi, 0o600) {
		t.Errorf("http.token: %v, %v; want mode 0600", fi, err)
	}
	call(t, home, &Request{Op: "append", Page: path, Author: "agent", Kind: "message", Text: "after"})
	if id := firstEventID(t, pageURL, "1"); id != "2" {
		t.Errorf("the old tab's stream resumes at %s, want 2", id)
	}
}

func TestATakenFixedPortStopsTheStartInsteadOfMoving(t *testing.T) {
	home := shortHome(t)
	port := fixPort(t, home)
	squatter, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = New(home).Serve(ctx)
	if err == nil || !strings.Contains(err.Error(), "port "+port+" from "+PortPath(home)+" is taken") {
		t.Fatalf("Serve = %v, want the taken port named", err)
	}
	if exists(HTTPURLPath(home)) {
		t.Errorf("a daemon that never served wrote http.url")
	}
}

func TestANewTokenOnAFixedPortSendsOldTabsTheRestartNotice(t *testing.T) {
	home := shortHome(t)
	fixPort(t, home)
	path := writePage(t, "<p>x</p>\n")
	_, stop := servingAt(t, home)
	pageURL := call(t, home, &Request{Op: "open", Page: path}).URL
	stop()
	if err := os.Remove(TokenPath(home)); err != nil {
		t.Fatal(err)
	}

	_, stop = servingAt(t, home)
	defer stop()
	before, err := url.Parse(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	after, err := url.Parse(httpURL(t, home))
	if err != nil {
		t.Fatal(err)
	}
	if after.Host != before.Host || strings.HasPrefix(before.Path, after.Path) {
		t.Fatalf("http.url = %s after a new token, old page %s; want the same port and a new token", after, before)
	}
	if got := reopenFor(t, pageURL); got != "kapi open "+path {
		t.Errorf("old tab gets reopen %q, want %q", got, "kapi open "+path)
	}
}

func TestAFixedPortReopensTabsOnlyWhenTheTokenChanges(t *testing.T) {
	home := shortHome(t)
	captureLog(t)
	fixPort(t, home)
	p := knownPage(t, home, "plan.html")

	markViewing(t, home, p, 0)
	o := &opener{}
	_, stop := servingWith(t, home, o.open)
	waitUntil(t, "the first start's reopen", func() bool { return len(o.opened()) == 1 })
	stop()

	markViewing(t, home, p, 0)
	o = &opener{}
	_, stop = servingWith(t, home, o.open)
	time.Sleep(100 * time.Millisecond)
	stop()
	if got := o.opened(); len(got) != 0 {
		t.Errorf("a restart with the same token reopened %v; the old tab reconnects by itself", got)
	}
	if exists(viewingFile(home, p)) {
		t.Errorf("viewing survived a start that kept the token")
	}
}

func TestClearingTheFixedPortGoesBackToANewTokenEveryStart(t *testing.T) {
	home := shortHome(t)
	fixPort(t, home)
	_, stop := servingAt(t, home)
	stop()
	if err := SetPort(home, 0); err != nil {
		t.Fatal(err)
	}
	if exists(PortPath(home)) {
		t.Fatalf("port 0 left %s", PortPath(home))
	}
	_, stop = servingAt(t, home)
	first := httpURL(t, home)
	stop()
	if exists(TokenPath(home)) {
		t.Errorf("http.token outlived the fixed port")
	}
	_, stop = servingAt(t, home)
	defer stop()
	if second := httpURL(t, home); second == first {
		t.Errorf("http.url %s kept its token without a fixed port", second)
	}
}

func TestAPortOutOfRangeOrABrokenPortFileIsRefused(t *testing.T) {
	home := shortHome(t)
	for _, n := range []int{-1, 65536} {
		if err := SetPort(home, n); err == nil {
			t.Errorf("SetPort(%d) succeeded", n)
		}
	}
	if err := os.WriteFile(PortPath(home), []byte("eighty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := New(home).Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not a port") {
		t.Errorf("Serve with a broken port file = %v, want it named", err)
	}
}
