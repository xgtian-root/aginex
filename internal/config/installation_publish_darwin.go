//go:build darwin

package config

import "golang.org/x/sys/unix"

func publishInstallation(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}
