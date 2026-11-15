//go:build !darwin && !linux

package trash

func systemCans(_, _ string) []can { return nil }
