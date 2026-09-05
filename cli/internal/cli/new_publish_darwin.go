//go:build darwin

package cli

import "golang.org/x/sys/unix"

func publishNewPath(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}
