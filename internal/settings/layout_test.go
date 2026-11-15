package settings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	pageA = "aaaaaaaaaaaaaaaa"
	pageB = "bbbbbbbbbbbbbbbb"
)

func layoutFile(t *testing.T, folders ...string) *File {
	t.Helper()
	known := Places{Folders: map[string]bool{}, Pages: map[string]bool{pageA: true, pageB: true}}
	for _, f := range folders {
		known.Folders[f] = true
	}
	return newFile(t).WithPlaces(func() (Places, error) { return known, nil })
}

func titles(pairs ...string) map[string]*string {
	out := map[string]*string{}
	for pair := range slices.Chunk(pairs, 2) {
		out[pair[0]] = ptr(pair[1])
	}
	return out
}

func TestLayoutPatchesMergePerKeyAndLeaveOtherSettingsAlone(t *testing.T) {
	f := layoutFile(t, "/work", "/home")
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Theme: ptr("paper"), Layout: &LayoutPatch{FolderTitles: titles("/work", "Work"), Collapsed: map[string]bool{"/work": true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Update(ctx, Patch{Layout: &LayoutPatch{FolderTitles: titles("/home", " Home "), Order: map[string][]string{"/home": {pageB, pageA}}}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Update(ctx, Patch{Mode: ptr(Dark)})
	if err != nil {
		t.Fatal(err)
	}
	want := Layout{
		FolderTitles: map[string]string{"/work": "Work", "/home": "Home"},
		PageTitles:   map[string]string{},
		Order:        map[string][]string{"/home": {pageB, pageA}},
		Collapsed:    map[string]bool{"/work": true},
	}
	if !equalLayout(got.Layout, want) || got.Theme != "paper" || got.Mode != Dark {
		t.Errorf("after three patches: %+v, want layout %+v with paper and dark", got, want)
	}
	reread, err := NewFile(f.path, f.known).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !equalLayout(reread.Layout, want) {
		t.Errorf("after reopening: %+v, want %+v", reread.Layout, want)
	}
}

func TestLayoutPatchesClearKeysWithNullEmptyOrFalse(t *testing.T) {
	f := layoutFile(t, "/work")
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Layout: &LayoutPatch{FolderTitles: titles("/work", "Work"), PageTitles: titles(pageA, "Plan"), Order: map[string][]string{"/work": {pageA}}, Collapsed: map[string]bool{"/work": true}}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Update(ctx, Patch{Layout: &LayoutPatch{FolderTitles: map[string]*string{"/work": nil}, PageTitles: titles(pageA, "  "), Order: map[string][]string{"/work": nil}, Collapsed: map[string]bool{"/work": false}}})
	if err != nil {
		t.Fatal(err)
	}
	if !equalLayout(got.Layout, EmptyLayout()) {
		t.Errorf("layout = %+v, want it empty", got.Layout)
	}
}

func TestLayoutPatchesRejectJunkAndKeepTheFile(t *testing.T) {
	f := layoutFile(t, "/work")
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Layout: &LayoutPatch{FolderTitles: titles("/work", "Work")}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	tooMany := make([]string, MaxOrder+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("%016x", i)
	}
	for name, p := range map[string]*LayoutPatch{
		"unknown folder title":    {FolderTitles: titles("/elsewhere", "X")},
		"unknown folder order":    {Order: map[string][]string{"/elsewhere": {pageA}}},
		"unknown folder collapse": {Collapsed: map[string]bool{"/elsewhere": true}},
		"unknown page title":      {PageTitles: titles("cccccccccccccccc", "X")},
		"long title":              {FolderTitles: titles("/work", strings.Repeat("x", MaxTitle+1))},
		"control character":       {FolderTitles: titles("/work", "a\nb")},
		"not a page id":           {Order: map[string][]string{"/work": {"../etc"}}},
		"repeated page id":        {Order: map[string][]string{"/work": {pageA, pageA}}},
		"long order":              {Order: map[string][]string{"/work": tooMany}},
		"valid next to junk":      {FolderTitles: titles("/work", "Fine", "/elsewhere", "X")},
	} {
		if _, err := f.Update(ctx, Patch{Theme: ptr("ocean"), Layout: p}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	after, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a rejected patch changed the file:\n%s\nwas:\n%s", after, before)
	}
}

func TestLayoutRefusesToGrowPastTheEntryLimit(t *testing.T) {
	folders := make([]string, MaxLayoutEntries+1)
	for i := range folders {
		folders[i] = fmt.Sprintf("/f%d", i)
	}
	f := layoutFile(t, folders...)
	ctx := context.Background()
	full := map[string]bool{}
	for _, folder := range folders[:MaxLayoutEntries] {
		full[folder] = true
	}
	if _, err := f.Update(ctx, Patch{Layout: &LayoutPatch{Collapsed: full}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Update(ctx, Patch{Layout: &LayoutPatch{Collapsed: map[string]bool{folders[MaxLayoutEntries]: true}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("one past the limit: err = %v, want ErrInvalid", err)
	}
	if _, err := f.Update(ctx, Patch{Layout: &LayoutPatch{Collapsed: map[string]bool{folders[0]: false, folders[MaxLayoutEntries]: true}}}); err != nil {
		t.Errorf("swapping one entry at the limit: %v", err)
	}
}

func TestConcurrentLayoutPatchesToDifferentFoldersAllLand(t *testing.T) {
	const n = 40
	folders := make([]string, n)
	for i := range folders {
		folders[i] = fmt.Sprintf("/folder-%d", i)
	}
	f := layoutFile(t, folders...)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i, folder := range folders {
		wg.Go(func() {
			_, err := f.Update(ctx, Patch{Layout: &LayoutPatch{FolderTitles: titles(folder, fmt.Sprint("Title ", i))}})
			errs <- err
		})
		wg.Go(func() {
			_, err := f.Update(ctx, Patch{Layout: &LayoutPatch{Collapsed: map[string]bool{folder: true}}})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Layout.FolderTitles) != n || len(got.Layout.Collapsed) != n {
		t.Fatalf("kept %d titles and %d collapsed folders, want %d of each", len(got.Layout.FolderTitles), len(got.Layout.Collapsed), n)
	}
	for i, folder := range folders {
		if got.Layout.FolderTitles[folder] != fmt.Sprint("Title ", i) {
			t.Errorf("%s titled %q", folder, got.Layout.FolderTitles[folder])
		}
	}
}

func TestImportFillsOnlyMissingKeysAndSkipsJunk(t *testing.T) {
	f := layoutFile(t, "/work", "/home")
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Layout: &LayoutPatch{FolderTitles: titles("/work", "Server name"), Order: map[string][]string{"/work": {pageB}}}}); err != nil {
		t.Fatal(err)
	}
	imported := &LayoutPatch{
		FolderTitles: titles("/work", "Browser name", "/home", "Home", "/gone", "Gone", "/blank", ""),
		PageTitles:   titles(pageA, "Plan", "cccccccccccccccc", "Unknown"),
		Order:        map[string][]string{"/work": {pageA, pageB}, "/home": {pageA, "junk"}},
		Collapsed:    map[string]bool{"/home": true, "/gone": true},
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := f.Update(ctx, Patch{Import: imported}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := f.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Layout{
		FolderTitles: map[string]string{"/work": "Server name", "/home": "Home"},
		PageTitles:   map[string]string{pageA: "Plan"},
		Order:        map[string][]string{"/work": {pageB}},
		Collapsed:    map[string]bool{"/home": true},
	}
	if !equalLayout(got.Layout, want) {
		t.Errorf("after two imports: %+v, want %+v", got.Layout, want)
	}
}

func TestLayoutWithoutKnownPlacesRejectsEveryKey(t *testing.T) {
	f := newFile(t)
	if _, err := f.Update(context.Background(), Patch{Layout: &LayoutPatch{Collapsed: map[string]bool{"/work": true}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestPlacesAreNotListedForPatchesWithoutLayout(t *testing.T) {
	f := newFile(t).WithPlaces(func() (Places, error) { return Places{}, errors.New("listing pages failed") })
	if _, err := f.Update(context.Background(), Patch{Mode: ptr(Dark)}); err != nil {
		t.Errorf("a theme patch failed on the page listing: %v", err)
	}
}

func TestAMangledLayoutReadsAsEmptyAndDropsBadEntries(t *testing.T) {
	f := newFile(t)
	for _, tc := range []struct {
		stored string
		want   Layout
	}{
		{`"nope"`, EmptyLayout()},
		{`{"folderTitles":{"/a":"A","/b":"` + strings.Repeat("x", MaxTitle+1) + `"},"pageTitles":{"x":"bad id","` + pageA + `":"Plan"},"order":{"/a":["` + pageA + `","` + pageA + `"],"/b":["` + pageB + `"]},"collapsed":{"/a":true,"/b":false}}`,
			Layout{FolderTitles: map[string]string{"/a": "A"}, PageTitles: map[string]string{pageA: "Plan"}, Order: map[string][]string{"/b": {pageB}}, Collapsed: map[string]bool{"/a": true}}},
	} {
		if err := os.WriteFile(f.path, []byte(`{"layout":`+tc.stored+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := f.Get(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !equalLayout(got.Layout, tc.want) {
			t.Errorf("stored %s read as %+v, want %+v", tc.stored, got.Layout, tc.want)
		}
	}
}

func equalLayout(a, b Layout) bool {
	return maps.Equal(a.FolderTitles, b.FolderTitles) && maps.Equal(a.PageTitles, b.PageTitles) && maps.Equal(a.Collapsed, b.Collapsed) &&
		maps.EqualFunc(a.Order, b.Order, slices.Equal) &&
		a.FolderTitles != nil && a.PageTitles != nil && a.Order != nil && a.Collapsed != nil
}
