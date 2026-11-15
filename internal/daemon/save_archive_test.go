package daemon

import (
	"testing"
	"time"

	"github.com/xaaha/kapi/internal/page"
)

func TestSavingAnArchivedPageBringsItBackAndTheSweepSkipsIt(t *testing.T) {
	home, s, _ := serving(t)
	withLibrary(t, s)
	from, _ := draftWithImage(t)
	call(t, home, &Request{Op: "open", Page: from})
	st, _, err := s.store(from)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := st.MarkArchived(time.Now(), st.Lifecycle().Touched); !ok || err != nil {
		t.Fatalf("MarkArchived = %v, %v", ok, err)
	}
	to, err := s.Save(from, "Work")
	if err != nil {
		t.Fatal(err)
	}
	after, err := page.ReadLifecycle(s.stateDir(to))
	if err != nil || !after.Saved || !after.Archived.IsZero() {
		t.Fatalf("lifecycle after saving an archived page = %+v, %v; want saved and not archived", after, err)
	}
	moved, _, err := s.store(to)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := moved.MarkArchived(time.Now(), moved.Lifecycle().Touched); ok {
		t.Error("a saved page was archived")
	}
	index, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 || !index[0].ArchivesAt.IsZero() || !index[0].ArchivedAt.IsZero() || !index[0].Saved {
		t.Errorf("index = %+v, want a saved page with no archive clock", index)
	}
}
