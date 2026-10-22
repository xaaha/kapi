package page

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReview51XattrOnHeldFileRevertsTheOp(t *testing.T) {
	home, path := newPage(t, r51start)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "aaaaaaaa"})
	after := onDisk(t, path)
	time.Sleep(10 * time.Millisecond)
	if err := unix.Setxattr(firstHeld(s), "com.example.tag", []byte("1"), 0); err != nil {
		t.Fatal(err)
	}
	tick(t, s, 1)
	if got := onDisk(t, path); got != after {
		t.Errorf("after an xattr on the held file, page = %q, want %q", got, after)
	}
}
