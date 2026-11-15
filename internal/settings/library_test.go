package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryDefaultsToKapiInHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got, err := newFile(t).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Library != "~/kapi" {
		t.Errorf("Library = %q, want ~/kapi", got.Library)
	}
	folders, err := got.Folders()
	if err != nil {
		t.Fatal(err)
	}
	want := Folders{Root: filepath.Join(home, "kapi"), Library: filepath.Join(home, "kapi", "library"), Drafts: filepath.Join(home, "kapi", "drafts")}
	if folders != want {
		t.Errorf("Folders = %+v, want %+v", folders, want)
	}
}

func TestLibraryPatches(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "notes")
	tests := []struct {
		in, want string
	}{
		{abs, abs},
		{abs + "/", abs},
		{"~/work/kapi/", "~/work/kapi"},
		{"~/a/../b", "~/b"},
		{"~", "~"},
		{"~/", "~"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			f := newFile(t)
			got, err := f.Update(context.Background(), Patch{Library: ptr(tt.in)})
			if err != nil {
				t.Fatal(err)
			}
			if got.Library != tt.want {
				t.Errorf("Update answered %q, want %q", got.Library, tt.want)
			}
			reread, err := NewFile(f.path, f.known).Get(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if reread.Library != tt.want {
				t.Errorf("after reopening: %q, want %q", reread.Library, tt.want)
			}
		})
	}
}

func TestLibraryPatchRefusesRelativeFoldersAndKeepsTheFile(t *testing.T) {
	for _, bad := range []string{"", "kapi", "./kapi", "~user/kapi", "../kapi"} {
		t.Run(bad, func(t *testing.T) {
			f := newFile(t)
			if _, err := f.Update(context.Background(), Patch{Theme: ptr("paper")}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Update(context.Background(), Patch{Library: ptr(bad)})
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "library") {
				t.Errorf("Update(%q) = %v, want ErrInvalid naming library", bad, err)
			}
			after, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Errorf("a refused update changed the file:\n%s", after)
			}
		})
	}
}

func TestHandEditedLibraryFallsBackWithOneWarning(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "notes")
	quoted, err := json.Marshal(abs + string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, library, want, warning string
	}{
		{"missing", ``, "~/kapi", ""},
		{"absolute", `,"library":` + string(quoted), abs, ""},
		{"relative", `,"library":"notes"`, "~/kapi", `library: "notes" is not an absolute folder`},
		{"number", `,"library":7`, "~/kapi", "library: 7 is not a string"},
		{"null", `,"library":null`, "~/kapi", "library: null is not a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFile(t)
			var logged []string
			f.logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
			if err := os.WriteFile(f.path, []byte(`{"mode":"dark","theme":"ocean","recent":["ocean"]`+tt.library+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				got, err := f.Get(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if got.Library != tt.want || got.Theme != "ocean" {
					t.Fatalf("Get = %+v, want ocean with library %q", got, tt.want)
				}
			}
			if tt.warning == "" {
				if len(logged) != 0 {
					t.Errorf("logged %q, want nothing", logged)
				}
				return
			}
			if len(logged) != 1 || !strings.Contains(logged[0], tt.warning) {
				t.Errorf("logged %q, want one warning with %q", logged, tt.warning)
			}
		})
	}
}

func TestFolderNamesWindowsCannotHoldAreRefused(t *testing.T) {
	for _, bad := range []string{"CON", "con", "Nul.txt", "aux ", "COM1", "lpt9.notes", "COM¹", "x.files.", "Work.", "Work/notes."} {
		if _, err := CheckFolder(bad); !errors.Is(err, ErrBadFolder) {
			t.Errorf("CheckFolder(%q) = %v, want ErrBadFolder", bad, err)
		}
	}
	for _, good := range []string{"Console", "COM", "COM10", "Lpt", "nullable", "Work/Clients", "v1.2"} {
		if _, err := CheckFolder(good); err != nil {
			t.Errorf("CheckFolder(%q) = %v, want it accepted", good, err)
		}
	}
}
