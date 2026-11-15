//go:build !windows

package osfile

import "testing"

func TestTheLinkAndPlaceholderFallbackAlsoRefusesAnythingAtTheTarget(t *testing.T) {
	renameCases(t, renameExclusive)
}
