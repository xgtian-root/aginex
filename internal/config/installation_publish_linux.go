//go:build linux

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func publishInstallation(source, destination string) error {
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
	return publishInstallationWithLink(source, destination)
}

func publishInstallationWithLink(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	// Publication has succeeded. A failed cleanup must not turn this into an
	// apparent uncommitted error; the caller's deferred cleanup retries it.
	_ = os.Remove(source)
	return nil
}
