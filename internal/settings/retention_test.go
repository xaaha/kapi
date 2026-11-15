package settings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

func TestRetentionDefaults(t *testing.T) {
	got, err := newFile(t).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Retention{ArchiveAfter: "24h", DeleteAfter: "720h", KeepWhileOpen: true}
	if got.Retention != want {
		t.Errorf("Retention = %+v, want %+v", got.Retention, want)
	}
	if p := got.Retention.Policy(); p != (Policy{ArchiveAfter: 24 * time.Hour, DeleteAfter: 720 * time.Hour, KeepWhileOpen: true}) {
		t.Errorf("Policy = %+v", p)
	}
}

func TestRetentionPatches(t *testing.T) {
	tests := []struct {
		name  string
		patch RetentionPatch
		want  Retention
		pol   Policy
	}{
		{"archive later", RetentionPatch{ArchiveAfter: ptr("48h")}, Retention{"48h", "720h", true}, Policy{48 * time.Hour, 720 * time.Hour, true}},
		{"delete sooner", RetentionPatch{DeleteAfter: ptr("90m")}, Retention{"24h", "90m", true}, Policy{24 * time.Hour, 90 * time.Minute, true}},
		{"archive off", RetentionPatch{ArchiveAfter: ptr("0")}, Retention{"0", "720h", true}, Policy{0, 720 * time.Hour, true}},
		{"delete off", RetentionPatch{DeleteAfter: ptr("0s")}, Retention{"24h", "0s", true}, Policy{24 * time.Hour, 0, true}},
		{"close does not protect", RetentionPatch{KeepWhileOpen: boolPtr(false)}, Retention{"24h", "720h", false}, Policy{24 * time.Hour, 720 * time.Hour, false}},
		{"everything", RetentionPatch{ptr("1h30m"), ptr("168h"), boolPtr(false)}, Retention{"1h30m", "168h", false}, Policy{90 * time.Minute, 168 * time.Hour, false}},
		{"empty", RetentionPatch{}, DefaultRetention(), DefaultRetention().Policy()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFile(t)
			ctx := context.Background()
			got, err := f.Update(ctx, Patch{Retention: &tt.patch})
			if err != nil {
				t.Fatal(err)
			}
			if got.Retention != tt.want {
				t.Errorf("Update answered %+v, want %+v", got.Retention, tt.want)
			}
			reread, err := NewFile(f.path, f.known).Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if reread.Retention != tt.want {
				t.Errorf("after reopening: %+v, want %+v", reread.Retention, tt.want)
			}
			if p := reread.Retention.Policy(); p != tt.pol {
				t.Errorf("Policy = %+v, want %+v", p, tt.pol)
			}
		})
	}
}

func TestRetentionPatchesRefuseBadDurationsAndKeepTheFile(t *testing.T) {
	tests := []struct {
		name  string
		patch RetentionPatch
		msg   string
	}{
		{"negative archive", RetentionPatch{ArchiveAfter: ptr("-1h")}, `retention.archiveAfter "-1h" is negative`},
		{"negative delete", RetentionPatch{DeleteAfter: ptr("-30m")}, `retention.deleteAfter "-30m" is negative`},
		{"days are not a unit", RetentionPatch{ArchiveAfter: ptr("1d")}, `retention.archiveAfter "1d" is not a duration`},
		{"bare number", RetentionPatch{DeleteAfter: ptr("30")}, `retention.deleteAfter "30" is not a duration`},
		{"empty", RetentionPatch{ArchiveAfter: ptr("")}, `retention.archiveAfter "" is not a duration`},
		{"words", RetentionPatch{DeleteAfter: ptr("forever")}, `retention.deleteAfter "forever" is not a duration`},
		{"one bad of two", RetentionPatch{ArchiveAfter: ptr("2h"), DeleteAfter: ptr("-1s"), KeepWhileOpen: boolPtr(false)}, `retention.deleteAfter "-1s" is negative`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFile(t)
			ctx := context.Background()
			if _, err := f.Update(ctx, Patch{Theme: ptr("paper")}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Update(ctx, Patch{Mode: ptr(Dark), Retention: &tt.patch})
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("Update = %v, want ErrInvalid containing %q", err, tt.msg)
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

func TestPartialRetentionPatchLeavesEverythingElseAlone(t *testing.T) {
	f := newFile(t)
	ctx := context.Background()
	if _, err := f.Update(ctx, Patch{Mode: ptr(Dark), Theme: ptr("ocean"), Retention: &RetentionPatch{DeleteAfter: ptr("48h"), KeepWhileOpen: boolPtr(false)}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Update(ctx, Patch{Retention: &RetentionPatch{ArchiveAfter: ptr("2h")}})
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{Mode: Dark, Theme: "ocean", Recent: []string{"ocean", "default"}, Retention: Retention{"2h", "48h", false}}
	if !equal(&got, &want) {
		t.Errorf("Update = %+v, want %+v", got, want)
	}
	got, err = f.Update(ctx, Patch{Theme: ptr("rose")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Retention != want.Retention {
		t.Errorf("a theme patch changed retention to %+v", got.Retention)
	}
}

func TestHandEditedRetentionFallsBackPerFieldWithOneWarning(t *testing.T) {
	tests := []struct {
		name      string
		retention string
		want      Retention
		warnings  []string
	}{
		{"missing block", ``, DefaultRetention(), nil},
		{"missing fields", `,"retention":{}`, DefaultRetention(), nil},
		{"all good", `,"retention":{"archiveAfter":"0","deleteAfter":"1h","keepWhileOpen":false}`, Retention{"0", "1h", false}, nil},
		{"bad archive", `,"retention":{"archiveAfter":"soon","deleteAfter":"1h","keepWhileOpen":false}`, Retention{"24h", "1h", false}, []string{`retention.archiveAfter: "soon" is not a duration`}},
		{"negative delete", `,"retention":{"archiveAfter":"2h","deleteAfter":"-5h"}`, Retention{"2h", "720h", true}, []string{`retention.deleteAfter: "-5h" is negative`}},
		{"number instead of string", `,"retention":{"archiveAfter":86400}`, DefaultRetention(), []string{`retention.archiveAfter: 86400 is not a string`}},
		{"null duration", `,"retention":{"deleteAfter":null}`, DefaultRetention(), []string{`retention.deleteAfter: null is not a string`}},
		{"string instead of bool", `,"retention":{"keepWhileOpen":"yes","archiveAfter":"3h"}`, Retention{"3h", "720h", true}, []string{`retention.keepWhileOpen: "yes" is not true or false`}},
		{"not an object", `,"retention":"off"`, DefaultRetention(), []string{`retention: "off" is not an object`}},
		{"two bad fields", `,"retention":{"archiveAfter":"x","deleteAfter":"y"}`, DefaultRetention(), []string{`retention.archiveAfter: "x"`, `retention.deleteAfter: "y"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFile(t)
			var logged []string
			f.logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
			contents := []byte(`{"mode":"dark","theme":"ocean","recent":["ocean"]` + tt.retention + `}`)
			if err := os.WriteFile(f.path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				got, err := f.Get(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if got.Retention != tt.want || got.Mode != Dark || got.Theme != "ocean" {
					t.Fatalf("Get = %+v, want dark ocean with retention %+v", got, tt.want)
				}
			}
			if len(logged) != len(tt.warnings) {
				t.Fatalf("logged %d warnings over three reads, want %d: %q", len(logged), len(tt.warnings), logged)
			}
			for i, w := range tt.warnings {
				if !strings.Contains(logged[i], w) || !strings.Contains(logged[i], f.path) {
					t.Errorf("warning %q does not name the file and %q", logged[i], w)
				}
			}
			after, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, contents) {
				t.Errorf("reading rewrote the file:\n%s", after)
			}
		})
	}
}

func TestPolicyFallsBackForValuesThatDidNotComeFromTheStore(t *testing.T) {
	got := Retention{ArchiveAfter: "nope", DeleteAfter: "-1h", KeepWhileOpen: false}.Policy()
	if want := (Policy{ArchiveAfter: 24 * time.Hour, DeleteAfter: 720 * time.Hour}); got != want {
		t.Errorf("Policy = %+v, want %+v", got, want)
	}
}
