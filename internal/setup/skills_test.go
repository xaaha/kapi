package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kapiskills "github.com/xaaha/kapi/skills"
)

func TestSkillsInstallGlobalPathsAndShareCodexWithPi(t *testing.T) {
	home := t.TempDir()
	o := SkillOptions{Home: home, Harnesses: Harnesses}
	results := InstallSkills(o)
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4 because Codex and Pi share one path: %+v", len(results), results)
	}
	paths := []string{
		filepath.Join(home, ".claude", "skills", "kapi", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "kapi", "SKILL.md"),
		filepath.Join(home, ".config", "opencode", "skills", "kapi", "SKILL.md"),
		filepath.Join(home, ".copilot", "skills", "kapi", "SKILL.md"),
	}
	for _, path := range paths {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, kapiskills.Kapi) {
			t.Errorf("%s = %q, %v", path, got, err)
		}
	}
	for _, r := range InstallSkills(o) {
		if r.Status != "unchanged" {
			t.Errorf("second install: %+v", r)
		}
	}
}

func TestProjectSkillsUsePortablePaths(t *testing.T) {
	root := t.TempDir()
	results := InstallSkills(SkillOptions{Project: root, Harnesses: []string{Claude, Codex, Pi}})
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, path := range []string{
		filepath.Join(root, ".claude", "skills", "kapi", "SKILL.md"),
		filepath.Join(root, ".agents", "skills", "kapi", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestRemoveSkillsOnlyRemovesUnchangedKapiFiles(t *testing.T) {
	home := t.TempDir()
	o := SkillOptions{Home: home, Harnesses: []string{Claude, Codex}}
	InstallSkills(o)
	claude, _, _ := skillPath(o, Claude)
	if err := os.WriteFile(claude, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	results := RemoveSkills(o)
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "did not write") {
		t.Errorf("Claude result = %+v", results[0])
	}
	if results[1].Status != "removed" {
		t.Errorf("Codex result = %+v", results[1])
	}
	if got, _ := os.ReadFile(claude); string(got) != "mine\n" {
		t.Errorf("changed skill was touched: %q", got)
	}
}
