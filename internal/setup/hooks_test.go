package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func opts(t *testing.T) Options {
	t.Helper()
	return Options{Home: t.TempDir(), Command: "kapi"}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func statuses(results []Result) map[string]string {
	out := map[string]string{}
	for _, r := range results {
		if r.Err != nil {
			out[r.Harness] = "error: " + r.Err.Error()
		} else {
			out[r.Harness] = r.Status
		}
	}
	return out
}

func TestInstallWritesEveryHarnessAndIsIdempotent(t *testing.T) {
	o := opts(t)
	got := statuses(Install(o))
	for _, h := range []string{"Claude Code", "Codex", "Codex features", "OpenCode", "Copilot CLI"} {
		if got[h] != "updated" {
			t.Errorf("%s: %s", h, got[h])
		}
	}
	claude, codexHooks, codexConfig, openCode, copilot := o.paths()
	wantHook := `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "kapi",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
`
	for _, p := range []string{claude, codexHooks} {
		if got := read(t, p); got != wantHook {
			t.Errorf("%s:\n%s", p, got)
		}
	}
	if got := read(t, codexConfig); got != "[features]\nhooks = true\n" {
		t.Errorf("codex config:\n%s", got)
	}
	plugin := read(t, openCode)
	for _, want := range []string{"// kapi managed opencode plugin: kapi", `const command = "kapi";`, `id: "axi-kapi"`, "async setup(ctx)", "async server({ directory })"} {
		if !strings.Contains(plugin, want) {
			t.Errorf("opencode plugin missing %q", want)
		}
	}
	var cp struct {
		Version int `json:"version"`
		Hooks   struct {
			SessionStart []map[string]any `json:"sessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, copilot)), &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Version != 1 || len(cp.Hooks.SessionStart) != 1 || cp.Hooks.SessionStart[0]["bash"] != "kapi hook-context" || cp.Hooks.SessionStart[0]["timeoutSec"] != float64(10) {
		t.Errorf("copilot = %+v", cp)
	}
	for h, s := range statuses(Install(o)) {
		if s != "unchanged" {
			t.Errorf("second install, %s: %s", h, s)
		}
	}
}

func TestInstallKeepsOtherSettingsAndRepairsItsOwnHook(t *testing.T) {
	o := opts(t)
	claude, _, _, _, _ := o.paths()
	write(t, claude, `{
  "model": "opus",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],
    "session_start": [{"command": "/old/kapi"}, {"command": "keep-me"}],
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "other-tool"}]},
      {"matcher": "", "hooks": [{"type": "command", "command": "/old/bin/kapi", "timeout": 5}]}
    ]
  },
  "zeta": 1
}`)
	if s := statuses(Install(o))["Claude Code"]; s != "updated" {
		t.Fatal(s)
	}
	got := read(t, claude)
	if strings.Index(got, `"model"`) > strings.Index(got, `"hooks"`) || strings.Index(got, `"hooks"`) > strings.Index(got, `"zeta"`) {
		t.Errorf("key order changed:\n%s", got)
	}
	for _, want := range []string{`"say done"`, `"other-tool"`, `"keep-me"`, `"command": "kapi"`, `"timeout": 10`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings missing %s:\n%s", want, got)
		}
	}
	for _, gone := range []string{"/old/bin/kapi", "/old/kapi"} {
		if strings.Contains(got, gone) {
			t.Errorf("stale %s kept:\n%s", gone, got)
		}
	}
	if strings.Count(got, `"command": "kapi"`) != 1 {
		t.Errorf("kapi hook duplicated:\n%s", got)
	}
}

func TestCodexConfigUpdate(t *testing.T) {
	cases := []struct {
		name, in, want string
		changed        bool
	}{
		{"empty", "", "[features]\nhooks = true\n", true},
		{"no features table", "model = \"x\"\n", "model = \"x\"\n\n[features]\nhooks = true\n", true},
		{"features without hooks", "[features]\nother = 1\n", "[features]\nother = 1\nhooks = true\n", true},
		{"features before another table", "[features]\nother = 1\n[profiles]\na = 1\n", "[features]\nother = 1\nhooks = true\n[profiles]\na = 1\n", true},
		{"hooks false", "[features]\nhooks = false\n", "[features]\nhooks = true\n", true},
		{"already on", "[features]\nhooks = true # yes\n", "[features]\nhooks = true # yes\n", false},
		{"hooks in another table", "[other]\nhooks = false\n", "[other]\nhooks = false\n\n[features]\nhooks = true\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := codexConfigUpdate(tc.in)
			if got != tc.want || changed != tc.changed {
				t.Errorf("got %q (changed=%v), want %q (changed=%v)", got, changed, tc.want, tc.changed)
			}
		})
	}
}

func TestRemoveDeletesOnlyKapiEntries(t *testing.T) {
	o := opts(t)
	claude, codexHooks, codexConfig, openCode, copilot := o.paths()
	write(t, claude, `{"hooks": {"SessionStart": [{"matcher": "", "hooks": [{"type": "command", "command": "other-tool"}]}]}}`)
	write(t, copilot, `{"version": 1, "hooks": {"sessionStart": [{"type": "command", "bash": "other-tool"}]}}`)
	Install(o)
	got := statuses(Remove(o))
	for h, want := range map[string]string{"Claude Code": "removed", "Codex": "removed", "OpenCode": "removed", "Copilot CLI": "removed"} {
		if got[h] != want {
			t.Errorf("%s: %s, want %s", h, got[h], want)
		}
	}
	if s := read(t, claude); strings.Contains(s, "kapi") || !strings.Contains(s, "other-tool") {
		t.Errorf("claude after remove:\n%s", s)
	}
	if s := read(t, codexHooks); strings.Contains(s, "kapi") || strings.Contains(s, "SessionStart") {
		t.Errorf("codex hooks after remove:\n%s", s)
	}
	if s := read(t, codexConfig); s != "[features]\nhooks = true\n" {
		t.Errorf("codex features flag must stay, got:\n%s", s)
	}
	if _, err := os.Stat(openCode); !os.IsNotExist(err) {
		t.Errorf("opencode plugin still there: %v", err)
	}
	if s := read(t, copilot); strings.Contains(s, "kapi") || !strings.Contains(s, "other-tool") {
		t.Errorf("copilot after remove:\n%s", s)
	}
}

func TestRemoveRefusesAnOpenCodePluginItDidNotWrite(t *testing.T) {
	o := opts(t)
	_, _, _, openCode, _ := o.paths()
	write(t, openCode, "export default {}\n")
	got := statuses(Remove(o))
	if !strings.HasPrefix(got["OpenCode"], "error: refusing") {
		t.Errorf("OpenCode: %s", got["OpenCode"])
	}
	if got["Claude Code"] != "absent" {
		t.Errorf("Claude Code: %s", got["Claude Code"])
	}
	if read(t, openCode) != "export default {}\n" {
		t.Error("unmanaged plugin changed")
	}
}

func TestInstallKeepsAnExistingFileMode(t *testing.T) {
	o := opts(t)
	claude, _, _, _, _ := o.paths()
	write(t, claude, "{}")
	if err := os.Chmod(claude, 0o644); err != nil {
		t.Fatal(err)
	}
	Install(o)
	info, err := os.Stat(claude)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestReviewH1TrailingContentIsAnErrorAndLeavesTheFile(t *testing.T) {
	for _, content := range []string{`{"a":1} junk`, `{"a":1}{"b":2}`} {
		o := opts(t)
		claude, _, _, _, _ := o.paths()
		write(t, claude, content)
		if s := statuses(Install(o))["Claude Code"]; !strings.HasPrefix(s, "error:") {
			t.Errorf("install %q: %s", content, s)
		}
		if s := statuses(Remove(o))["Claude Code"]; !strings.HasPrefix(s, "error:") {
			t.Errorf("remove %q: %s", content, s)
		}
		if read(t, claude) != content {
			t.Errorf("%q rewritten to %q", content, read(t, claude))
		}
	}
}

func TestReviewH2InstallRefusesAnOpenCodePluginItDidNotWrite(t *testing.T) {
	o := opts(t)
	_, _, _, openCode, _ := o.paths()
	write(t, openCode, "export default {}\n")
	if s := statuses(Install(o))["OpenCode"]; !strings.HasPrefix(s, "error: refusing") {
		t.Errorf("OpenCode: %s", s)
	}
	if read(t, openCode) != "export default {}\n" {
		t.Error("unmanaged plugin overwritten")
	}
}

func TestReviewH3OtherValuesKeepHTMLCharacters(t *testing.T) {
	o := opts(t)
	claude, _, _, _, copilot := o.paths()
	const rule = `"Bash(a && b <c> d)"`
	write(t, claude, `{"permissions": {"allow": [`+rule+`]}}`)
	write(t, copilot, `{"hooks": {"sessionStart": [{"bash": "x && y <z>"}]}}`)
	Install(o)
	if s := read(t, claude); !strings.Contains(s, rule) {
		t.Errorf("claude settings:\n%s", s)
	}
	if s := read(t, copilot); !strings.Contains(s, `"x && y <z>"`) {
		t.Errorf("copilot hooks:\n%s", s)
	}
}

func TestReviewH4MalformedManagedEntriesAreRepaired(t *testing.T) {
	for _, entry := range []string{
		`{"type": "command", "command": "kapi", "timeout": "10"}`,
		`{"type": "prompt", "command": "kapi", "timeout": 10}`,
		`{"type": "command", "command": "kapi", "timeout": 10.5}`,
		`{"type": 1, "command": "kapi"}`,
	} {
		o := opts(t)
		claude, _, _, _, _ := o.paths()
		write(t, claude, `{"hooks": {"SessionStart": [{"matcher": "", "hooks": [`+entry+`]}]}}`)
		if s := statuses(Install(o))["Claude Code"]; s != "updated" {
			t.Errorf("%s: %s", entry, s)
		}
		got := read(t, claude)
		if !strings.Contains(got, `"type": "command"`) || !strings.Contains(got, `"timeout": 10`+"\n") {
			t.Errorf("%s not repaired:\n%s", entry, got)
		}
	}
}

func TestReviewH6OnlyKapiCommandsCountAsManaged(t *testing.T) {
	o := opts(t)
	claude, _, _, _, copilot := o.paths()
	others := []string{"/x/kapi-tools/run.sh", "kapi-other", "mykapi", "kapi watch plan.html"}
	var claudeEntries, copilotEntries []string
	for _, c := range others {
		claudeEntries = append(claudeEntries, `{"type": "command", "command": "`+c+`"}`)
		copilotEntries = append(copilotEntries, `{"type": "command", "bash": "`+c+`"}`)
	}
	write(t, claude, `{"hooks": {"SessionStart": [{"matcher": "", "hooks": [`+strings.Join(claudeEntries, ",")+`]}]}}`)
	write(t, copilot, `{"version": 1, "hooks": {"sessionStart": [`+strings.Join(copilotEntries, ",")+`]}}`)
	Install(o)
	Remove(o)
	for _, p := range []string{claude, copilot} {
		got := read(t, p)
		for _, c := range others {
			if !strings.Contains(got, `"`+c+`"`) {
				t.Errorf("%s lost %q:\n%s", p, c, got)
			}
		}
	}
}
