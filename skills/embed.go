// Package skills exposes the agent skill bundled with the kapi binary.
package skills

import _ "embed"

// Kapi is the portable Agent Skills file installed by kapi setup.
//
//go:embed kapi/SKILL.md
var Kapi []byte
