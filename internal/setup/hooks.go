package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Marker identifies hook entries and files that kapi manages.
const Marker = "kapi"

const openCodeManagedPrefix = "kapi managed opencode plugin:"

var errUnmanagedOpenCode = errors.New("refusing to change an OpenCode plugin kapi did not write")

// Options says where the agent harnesses keep their settings and which
// command the hooks run.
type Options struct {
	Home        string
	CopilotHome string
	Command     string
	Timeout     int
}

// Result reports what happened to one harness's settings.
type Result struct {
	Harness string
	Path    string
	Status  string
	Err     error
}

func (o Options) paths() (claude, codexHooks, codexConfig, openCode, copilot string) {
	copilotHome := o.CopilotHome
	if copilotHome == "" {
		copilotHome = filepath.Join(o.Home, ".copilot")
	}
	return filepath.Join(o.Home, ".claude", "settings.json"),
		filepath.Join(o.Home, ".codex", "hooks.json"),
		filepath.Join(o.Home, ".codex", "config.toml"),
		filepath.Join(o.Home, ".config", "opencode", "plugins", "axi-kapi.js"),
		filepath.Join(copilotHome, "hooks", "kapi.json")
}

func (o Options) timeout() int {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 10
}

// Install adds or repairs kapi's SessionStart hook in every supported harness.
// One harness failing never stops the others.
func Install(o Options) []Result {
	claude, codexHooks, codexConfig, openCode, copilot := o.paths()
	return []Result{
		rewriteFile("Claude Code", claude, func(b []byte) ([]byte, bool, error) {
			return sessionStartUpdate(b, o.Command, o.timeout())
		}),
		rewriteFile("Codex", codexHooks, func(b []byte) ([]byte, bool, error) {
			return sessionStartUpdate(b, o.Command, o.timeout())
		}),
		rewriteFile("Codex features", codexConfig, func(b []byte) ([]byte, bool, error) {
			s, changed := codexConfigUpdate(string(b))
			return []byte(s), changed, nil
		}),
		rewriteFile("OpenCode", openCode, func(b []byte) ([]byte, bool, error) {
			if len(b) > 0 && !bytes.Contains(b, []byte(openCodeManagedPrefix)) {
				return nil, false, errUnmanagedOpenCode
			}
			next := []byte(openCodePlugin(o.Command, o.timeout()))
			return next, !bytes.Equal(b, next), nil
		}),
		rewriteFile("Copilot CLI", copilot, func(b []byte) ([]byte, bool, error) {
			return copilotUpdate(b, o.Command+" hook-context", o.timeout())
		}),
	}
}

// Remove deletes only kapi-managed hook entries and files. The Codex
// [features].hooks flag is shared with other tools and is left alone.
func Remove(o Options) []Result {
	claude, codexHooks, _, openCode, copilot := o.paths()
	results := []Result{
		removeFromFile("Claude Code", claude, sessionStartRemoval),
		removeFromFile("Codex", codexHooks, sessionStartRemoval),
		removeFromFile("Copilot CLI", copilot, copilotRemoval),
	}
	r := Result{Harness: "OpenCode", Path: openCode}
	b, err := os.ReadFile(openCode)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.Status = "absent"
	case err != nil:
		r.Err = err
	case !strings.Contains(string(b), openCodeManagedPrefix):
		r.Err = errUnmanagedOpenCode
	default:
		r.Err = os.Remove(openCode)
		r.Status = "removed"
	}
	return append(results, r)
}

func rewriteFile(harness, path string, update func([]byte) ([]byte, bool, error)) Result {
	r := Result{Harness: harness, Path: path}
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		r.Err = err
		return r
	}
	next, changed, err := update(current)
	if err != nil {
		r.Err = err
		return r
	}
	if !changed {
		r.Status = "unchanged"
		return r
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		r.Err = err
		return r
	}
	if err := writeKeepingMode(path, next); err != nil {
		r.Err = err
		return r
	}
	r.Status = "updated"
	return r
}

func removeFromFile(harness, path string, remove func([]byte) ([]byte, bool, error)) Result {
	r := Result{Harness: harness, Path: path}
	current, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		r.Status = "absent"
		return r
	}
	if err != nil {
		r.Err = err
		return r
	}
	next, changed, err := remove(current)
	if err != nil {
		r.Err = err
		return r
	}
	if !changed {
		r.Status = "unchanged"
		return r
	}
	if err := writeKeepingMode(path, next); err != nil {
		r.Err = err
		return r
	}
	r.Status = "removed"
	return r
}

func writeKeepingMode(path string, b []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, b, mode)
}

func loadObject(b []byte) (*object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return newObject(), nil
	}
	return parseObject(b)
}

func array(o *object, key string) ([]json.RawMessage, bool, error) {
	raw, ok := o.get(key)
	if !ok {
		return nil, false, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, true, fmt.Errorf("%s is not an array: %w", key, err)
	}
	return items, true, nil
}

func childObject(o *object, key string) (*object, bool, error) {
	raw, ok := o.get(key)
	if !ok {
		return newObject(), false, nil
	}
	child, err := parseObject(raw)
	if err != nil {
		return nil, true, fmt.Errorf("%s is not an object: %w", key, err)
	}
	return child, true, nil
}

func stringField(raw json.RawMessage, keys ...string) []string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	var out []string
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func isManaged(raw json.RawMessage, keys ...string) bool {
	for _, s := range stringField(raw, keys...) {
		if isKapiCommand(s) {
			return true
		}
	}
	return false
}

func isKapiCommand(s string) bool {
	s = strings.TrimSuffix(strings.TrimSpace(s), " hook-context")
	return filepath.Base(s) == Marker
}

func withoutManaged(items []json.RawMessage, keys ...string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		if !isManaged(it, keys...) {
			out = append(out, it)
		}
	}
	return out
}

func sessionStartUpdate(content []byte, command string, timeout int) ([]byte, bool, error) {
	root, err := loadObject(content)
	if err != nil {
		return nil, false, err
	}
	hooks, had, err := childObject(root, "hooks")
	if err != nil {
		return nil, false, err
	}
	changed := !had
	legacy, hadLegacy, err := array(hooks, "session_start")
	if err != nil {
		return nil, false, err
	}
	if hadLegacy {
		kept := withoutManaged(legacy, "command")
		if len(kept) != len(legacy) {
			changed = true
			if len(kept) == 0 {
				hooks.del("session_start")
			} else if err := hooks.setValue("session_start", kept); err != nil {
				return nil, false, err
			}
		}
	}
	groups, hadGroups, err := array(hooks, "SessionStart")
	if err != nil {
		return nil, false, err
	}
	changed = changed || !hadGroups
	found := false
	for gi, g := range groups {
		gobj, err := parseObject(g)
		if err != nil {
			continue
		}
		entries, ok, err := array(gobj, "hooks")
		if err != nil || !ok {
			continue
		}
		for hi, h := range entries {
			if !isManaged(h, "command") {
				continue
			}
			hobj, err := parseObject(h)
			if err != nil {
				return nil, false, err
			}
			var cur map[string]any
			if err := json.Unmarshal(h, &cur); err != nil {
				return nil, false, err
			}
			if cur["type"] == "command" && cur["command"] == command && cur["timeout"] == float64(timeout) && !changed {
				return content, false, nil
			}
			for k, v := range map[string]any{"type": "command", "command": command, "timeout": timeout} {
				if err := hobj.setValue(k, v); err != nil {
					return nil, false, err
				}
			}
			hb, err := marshal(hobj)
			if err != nil {
				return nil, false, err
			}
			entries[hi] = hb
			if err := gobj.setValue("hooks", entries); err != nil {
				return nil, false, err
			}
			gb, err := marshal(gobj)
			if err != nil {
				return nil, false, err
			}
			groups[gi] = gb
			found = true
			break
		}
		if found {
			break
		}
	}
	if !found {
		group := newObject()
		entry := newObject()
		for _, kv := range []struct {
			k string
			v any
		}{{"type", "command"}, {"command", command}, {"timeout", timeout}} {
			if err := entry.setValue(kv.k, kv.v); err != nil {
				return nil, false, err
			}
		}
		if err := group.setValue("matcher", ""); err != nil {
			return nil, false, err
		}
		if err := group.setValue("hooks", []*object{entry}); err != nil {
			return nil, false, err
		}
		gb, err := marshal(group)
		if err != nil {
			return nil, false, err
		}
		groups = append(groups, gb)
	}
	if err := hooks.setValue("SessionStart", groups); err != nil {
		return nil, false, err
	}
	if err := root.setValue("hooks", hooks); err != nil {
		return nil, false, err
	}
	out, err := indent(root)
	return out, true, err
}

func sessionStartRemoval(content []byte) ([]byte, bool, error) {
	root, err := loadObject(content)
	if err != nil {
		return nil, false, err
	}
	hooks, had, err := childObject(root, "hooks")
	if err != nil || !had {
		return content, false, err
	}
	changed := false
	if legacy, ok, err := array(hooks, "session_start"); err != nil {
		return nil, false, err
	} else if ok {
		kept := withoutManaged(legacy, "command")
		if len(kept) != len(legacy) {
			changed = true
			if len(kept) == 0 {
				hooks.del("session_start")
			} else if err := hooks.setValue("session_start", kept); err != nil {
				return nil, false, err
			}
		}
	}
	if groups, ok, err := array(hooks, "SessionStart"); err != nil {
		return nil, false, err
	} else if ok {
		var keptGroups []json.RawMessage
		for _, g := range groups {
			gobj, err := parseObject(g)
			if err != nil {
				keptGroups = append(keptGroups, g)
				continue
			}
			entries, ok, err := array(gobj, "hooks")
			if err != nil || !ok {
				keptGroups = append(keptGroups, g)
				continue
			}
			kept := withoutManaged(entries, "command")
			if len(kept) == len(entries) {
				keptGroups = append(keptGroups, g)
				continue
			}
			changed = true
			if len(kept) == 0 {
				continue
			}
			if err := gobj.setValue("hooks", kept); err != nil {
				return nil, false, err
			}
			gb, err := marshal(gobj)
			if err != nil {
				return nil, false, err
			}
			keptGroups = append(keptGroups, gb)
		}
		if len(keptGroups) == 0 {
			hooks.del("SessionStart")
		} else if err := hooks.setValue("SessionStart", keptGroups); err != nil {
			return nil, false, err
		}
	}
	if !changed {
		return content, false, nil
	}
	if len(hooks.keys) == 0 {
		root.del("hooks")
	} else if err := root.setValue("hooks", hooks); err != nil {
		return nil, false, err
	}
	out, err := indent(root)
	return out, true, err
}

func copilotUpdate(content []byte, command string, timeout int) ([]byte, bool, error) {
	root, err := loadObject(content)
	if err != nil {
		return nil, false, err
	}
	hooks, _, err := childObject(root, "hooks")
	if err != nil {
		return nil, false, err
	}
	current, _, err := array(hooks, "sessionStart")
	if err != nil {
		return nil, false, err
	}
	entry := newObject()
	for _, kv := range []struct {
		k string
		v any
	}{{"type", "command"}, {"bash", command}, {"powershell", command}, {"timeoutSec", timeout}} {
		if err := entry.setValue(kv.k, kv.v); err != nil {
			return nil, false, err
		}
	}
	eb, err := marshal(entry)
	if err != nil {
		return nil, false, err
	}
	next := append(withoutManaged(current, "bash", "powershell", "command"), eb)
	if err := root.setValue("version", 1); err != nil {
		return nil, false, err
	}
	if err := hooks.setValue("sessionStart", next); err != nil {
		return nil, false, err
	}
	if err := root.setValue("hooks", hooks); err != nil {
		return nil, false, err
	}
	out, err := indent(root)
	if err != nil {
		return nil, false, err
	}
	return out, !bytes.Equal(out, content), nil
}

func copilotRemoval(content []byte) ([]byte, bool, error) {
	root, err := loadObject(content)
	if err != nil {
		return nil, false, err
	}
	hooks, had, err := childObject(root, "hooks")
	if err != nil || !had {
		return content, false, err
	}
	current, ok, err := array(hooks, "sessionStart")
	if err != nil || !ok {
		return content, false, err
	}
	kept := withoutManaged(current, "bash", "powershell", "command")
	if len(kept) == len(current) {
		return content, false, nil
	}
	if err := hooks.setValue("sessionStart", kept); err != nil {
		return nil, false, err
	}
	if err := root.setValue("hooks", hooks); err != nil {
		return nil, false, err
	}
	out, err := indent(root)
	return out, true, err
}

var (
	tomlSection = regexp.MustCompile(`^\s*(\[{1,2})([^\]]+)(\]{1,2})\s*(?:#.*)?$`)
	tomlHooks   = regexp.MustCompile(`^\s*hooks\s*=\s*(true|false)\s*(?:#.*)?$`)
)

// codexConfigUpdate turns on Codex's [features].hooks flag, which Codex needs
// before it runs any hook.
func codexConfigUpdate(content string) (string, bool) {
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}
	if strings.TrimSpace(content) == "" {
		return "[features]" + nl + "hooks = true" + nl, true
	}
	lines := regexp.MustCompile(`\r?\n`).Split(content, -1)
	inFeatures, sawFeatures := false, false
	for i, line := range lines {
		if m := tomlSection.FindStringSubmatch(line); m != nil {
			if (m[1] != "[" || m[3] != "]") && (m[1] != "[[" || m[3] != "]]") {
				continue
			}
			if inFeatures {
				out := append(append(append([]string{}, lines[:i]...), "hooks = true"), lines[i:]...)
				return strings.Join(out, nl), true
			}
			inFeatures = strings.TrimSpace(m[2]) == "features"
			sawFeatures = sawFeatures || inFeatures
			continue
		}
		if !inFeatures {
			continue
		}
		if m := tomlHooks.FindStringSubmatch(line); m != nil {
			if m[1] == "true" {
				return content, false
			}
			lines[i] = strings.Replace(line, "false", "true", 1)
			return strings.Join(lines, nl), true
		}
	}
	if sawFeatures {
		suffix := ""
		if !strings.HasSuffix(content, nl) {
			suffix = nl
		}
		return content + suffix + "hooks = true" + nl, true
	}
	sep := nl + nl
	if strings.HasSuffix(content, nl) {
		sep = nl
	}
	return content + sep + "[features]" + nl + "hooks = true" + nl, true
}
