package skills

import (
	"strings"
	"testing"
)

func TestKapiSkillPromptsProactiveVisuals(t *testing.T) {
	skill := string(Kapi)
	for _, want := range []string{
		"Do not wait for the user to ask for a visual",
		"Use `kapi-diagram` for flows, sequences, architecture, dependencies, state changes, and relationships",
		"Use `kapi-render` with ordinary HTML and CSS",
		"never as a substitute for a diagram or visual interface",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("embedded skill is missing %q", want)
		}
	}
}
