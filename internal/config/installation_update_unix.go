//go:build unix

package config

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockInstallationUpdate(path string) (func(), error) {
	fd, err := unix.Open(
		path+".lock",
		unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("open installation configuration lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	if lock == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open installation configuration lock")
	}
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = lock.Close()
		return nil, fmt.Errorf("installation configuration lock must be a private regular file")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock installation configuration: %w", err)
	}
	return func() {
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}, nil
}
