package settings

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

var themes = []string{"default", "paper", "ocean", "slate", "rose"}

func newFile(t *testing.T) *File {
	t.Helper()
	return NewFile(filepath.Join(t.TempDir(), "settings.json"), func(id string) bool { return slices.Contains(themes, id) })
}

func ptr(s string) *string { return &s }

func TestMissingFileReadsAsDefaults(t *testing.T) {
	got, err := newFile(t).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := Default(); !equal(got, want) {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestUpdateRoundTripsThroughANewStore(t *testing.T) {
	f := newFile(t)
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Mode: ptr(Dark), Theme: ptr("ocean")}); err != nil {
		t.Fatal(err)
	}
	got, err := NewFile(f.path, f.known).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{Mode: Dark, Theme: "ocean", Recent: []string{"ocean", "default"}}
	if !equal(got, want) {
		t.Errorf("after reopening: %+v, want %+v", got, want)
	}
}

func TestPatchLeavesUnsetFieldsAlone(t *testing.T) {
	f := newFile(t)
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Theme: ptr("paper")}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Update(ctx, Patch{Mode: ptr(Light)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != "paper" || got.Mode != Light {
		t.Errorf("got %+v, want paper in light mode", got)
	}
}

func TestRecentKeepsTheLastThreeThemesNewestFirst(t *testing.T) {
	f := newFile(t)
	ctx := context.Background()
	var got Settings
	for _, id := range []string{"paper", "ocean", "slate", "paper", "rose"} {
		var err error
		if got, err = f.Update(ctx, Patch{Theme: ptr(id)}); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"rose", "paper", "slate"}; !slices.Equal(got.Recent, want) {
		t.Errorf("Recent = %v, want %v", got.Recent, want)
	}
}

func TestUpdateRefusesUnknownValuesAndKeepsTheFile(t *testing.T) {
	f := newFile(t)
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Theme: ptr("paper")}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Patch{{Mode: ptr("sepia")}, {Theme: ptr("neon")}, {Mode: ptr(Dark), Theme: ptr("")}} {
		if _, err := f.Update(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("Update(%+v) = %v, want ErrInvalid", p, err)
		}
	}
	after, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("a refused update changed the file:\n%s", after)
	}
}

func TestAFileFromAnOlderCatalogFallsBackToKnownThemes(t *testing.T) {
	f := newFile(t)
	if err := os.WriteFile(f.path, []byte(`{"mode":"sepia","theme":"gone","recent":["gone","rose","paper","ocean"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := f.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{Mode: System, Theme: "default", Recent: []string{"default", "rose", "paper"}}
	if !equal(got, want) {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestACorruptFileIsAnError(t *testing.T) {
	f := newFile(t)
	if err := os.WriteFile(f.path, []byte(`{"mode":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(context.Background()); err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Errorf("Get = %v, want an error naming the file", err)
	}
}

func TestUpdatesWriteAtomicallyAndLeaveNoTempFiles(t *testing.T) {
	f := newFile(t)
	ctx := context.Background()
	for _, id := range themes {
		if _, err := f.Update(ctx, Patch{Theme: ptr(id)}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(f.path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only settings.json", names)
	}
	info, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("settings.json mode %v, want 0600", perm)
	}
}

func TestConcurrentUpdatesAllLand(t *testing.T) {
	f := newFile(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			p := Patch{Theme: ptr(themes[i%len(themes)])}
			if i%2 == 0 {
				p = Patch{Mode: ptr(Modes[i%len(Modes)])}
			}
			if _, err := f.Update(ctx, p); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := NewFile(f.path, f.known).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(Modes, got.Mode) || !slices.Contains(themes, got.Theme) || len(got.Recent) != MaxRecent || got.Recent[0] != got.Theme {
		t.Errorf("after concurrent updates: %+v", got)
	}
}

func equal(a, b Settings) bool {
	return a.Mode == b.Mode && a.Theme == b.Theme && slices.Equal(a.Recent, b.Recent)
}
