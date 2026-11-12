package skills

import (
	"strings"
	"testing"
)

func TestKapiSkillIsConciseAndVisual(t *testing.T) {
	skill := string(Kapi)
	if words := len(strings.Fields(skill)); words > 350 {
		t.Fatalf("embedded skill has %d words; want at most 350", words)
	}
	for _, want := range []string{
		"Add visuals without being asked",
		"Use `kapi-diagram`",
		"Use `kapi-render`",
		"never instead of a diagram or interface",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("embedded skill is missing %q", want)
		}
	}
}
