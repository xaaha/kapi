package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionCommandAndRootFlagsPrintTheVersion(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })
	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		out, err := run(t, &fakeAPI{}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if out != "1.2.3\n" {
			t.Errorf("%v output = %q, want %q", args, out, "1.2.3\n")
		}
	}
}

func TestResolveVersion(t *testing.T) {
	info := func(v string, ok bool) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			if !ok {
				return nil, false
			}
			return &debug.BuildInfo{Main: debug.Module{Version: v}}, true
		}
	}
	cases := []struct {
		name    string
		ldflags string
		build   func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"ldflags wins", "1.2.3", info("v0.1.0", true), "1.2.3"},
		{"go install module version", "dev", info("v0.1.0", true), "v0.1.0"},
		{"local devel build", "dev", info("(devel)", true), "dev"},
		{"empty module version", "dev", info("", true), "dev"},
		{"no build info", "dev", info("", false), "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.ldflags, tc.build); got != tc.want {
				t.Errorf("resolveVersion = %q, want %q", got, tc.want)
			}
		})
	}
}
