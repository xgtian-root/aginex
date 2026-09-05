//go:build linux

package cli

import (
	"errors"

	"golang.org/x/sys/unix"
)

func publishNewPath(source, destination string) error {
	err := unix.Renameat2(
		unix.AT_FDCWD,
		source,
		unix.AT_FDCWD,
		destination,
		unix.RENAME_NOREPLACE,
	)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.ENOSYS) &&
		!errors.Is(err, unix.EINVAL) &&
		!errors.Is(err, unix.EOPNOTSUPP) {
		return err
	}
	return publishNewPathFallback(source, destination)
}
