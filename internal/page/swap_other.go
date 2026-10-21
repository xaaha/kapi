//go:build !darwin && !linux

package page

func swapFiles(_, _ string) error { return errNoSwap }
