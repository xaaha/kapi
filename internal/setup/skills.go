package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	kapiskills "github.com/xaaha/kapi/skills"
)

// Harness names accepted by kapi setup.
const (
	Claude   = "claude"
	Codex    = "codex"
	Pi       = "pi"
	OpenCode = "opencode"
	Copilot  = "copilot"
)

var Harnesses = []string{Claude, Codex, Pi, OpenCode, Copilot}

// Selection is a compact set of selected harnesses. Zero means every harness
// for backwards-compatible setup calls.
type Selection uint8

// Select builds a harness selection from validated harness names.
func Select(names []string) Selection {
	var selected Selection
	for _, name := range names {
		if i := slices.Index(Harnesses, name); i >= 0 {
			selected |= 1 << i
		}
	}
	return selected
}

func (s Selection) has(name string) bool {
	if s == 0 {
		return true
	}
	i := slices.Index(Harnesses, name)
	return i >= 0 && s&(1<<i) != 0
}

// SkillOptions selects the harness skill directories. Project is empty for
// global installation; otherwise it is the project root.
type SkillOptions struct {
	Home        string
	CopilotHome string
	Project     string
	Harnesses   []string
}

// InstallSkills writes or repairs the embedded kapi skill. Codex and Pi share
// the Agent Skills path, so it is reported and written only once.
func InstallSkills(o SkillOptions) []Result { return changeSkills(o, false) }

// RemoveSkills deletes only SKILL.md files whose content is kapi's embedded
// skill, leaving changed or user-owned files alone.
func RemoveSkills(o SkillOptions) []Result { return changeSkills(o, true) }

func changeSkills(o SkillOptions, remove bool) []Result {
	selected := o.Harnesses
	if len(selected) == 0 {
		selected = Harnesses
	}
	seen := map[string]bool{}
	var results []Result
	for _, harness := range selected {
		path, label, err := skillPath(o, harness)
		if err != nil {
			results = append(results, Result{Harness: harness, Err: err})
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		r := Result{Harness: label + " skill", Path: path}
		current, err := os.ReadFile(path)
		switch {
		case remove && errors.Is(err, os.ErrNotExist):
			r.Status = "absent"
		case remove && err != nil:
			r.Err = err
		case remove && !isKapiSkill(current):
			r.Err = fmt.Errorf("refusing to remove a skill kapi did not write")
		case remove:
			r.Err = os.Remove(path)
			if r.Err == nil {
				r.Status = "removed"
				_ = os.Remove(filepath.Dir(path))
			}
		case err != nil && !errors.Is(err, os.ErrNotExist):
			r.Err = err
		case bytes.Equal(current, kapiskills.Kapi):
			r.Status = "unchanged"
		default:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				r.Err = err
			} else if err := os.WriteFile(path, kapiskills.Kapi, 0o600); err != nil {
				r.Err = err
			} else {
				r.Status = "updated"
			}
		}
		results = append(results, r)
	}
	return results
}

func isKapiSkill(content []byte) bool {
	return bytes.HasPrefix(content, []byte("---\n")) &&
		bytes.Contains(content, []byte("\nname: kapi\n")) &&
		bytes.Contains(content, []byte("\n  author: Pratik Thapa (xaaha)\n"))
}

func skillPath(o SkillOptions, harness string) (path, label string, err error) {
	if !slices.Contains(Harnesses, harness) {
		return "", "", fmt.Errorf("unknown harness %q", harness)
	}
	if o.Project != "" {
		switch harness {
		case Claude:
			return filepath.Join(o.Project, ".claude", "skills", "kapi", "SKILL.md"), "Claude Code", nil
		case Codex, Pi:
			return filepath.Join(o.Project, ".agents", "skills", "kapi", "SKILL.md"), "Codex/Pi", nil
		default:
			return "", "", fmt.Errorf("%s has no portable project skill path", harness)
		}
	}
	copilotHome := o.CopilotHome
	if copilotHome == "" {
		copilotHome = filepath.Join(o.Home, ".copilot")
	}
	switch harness {
	case Claude:
		return filepath.Join(o.Home, ".claude", "skills", "kapi", "SKILL.md"), "Claude Code", nil
	case Codex, Pi:
		return filepath.Join(o.Home, ".agents", "skills", "kapi", "SKILL.md"), "Codex/Pi", nil
	case OpenCode:
		return filepath.Join(o.Home, ".config", "opencode", "skills", "kapi", "SKILL.md"), "OpenCode", nil
	case Copilot:
		return filepath.Join(copilotHome, "skills", "kapi", "SKILL.md"), "Copilot", nil
	}
	panic("unreachable")
}
