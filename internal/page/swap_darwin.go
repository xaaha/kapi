package page

import (
	"errors"

	"golang.org/x/sys/unix"
)

func exchangeFiles(a, b string) error {
	err := unix.RenamexNp(a, b, unix.RENAME_SWAP)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		return errNoSwap
	}
	return err
}
