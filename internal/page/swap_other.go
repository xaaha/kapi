//go:build !darwin && !linux

package page

func exchangeFiles(_, _ string) error { return errNoSwap }
