//go:build unix

package cli

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func configureProcessTree(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func stopProcessTree(cmd *exec.Cmd, done <-chan struct{}) {
	select {
	case <-done:
		return
	default:
	}

	_ = unix.Kill(-cmd.Process.Pid, unix.SIGINT)
	grace := 15 * time.Second
	for _, entry := range cmd.Env {
		key, value, _ := strings.Cut(entry, "=")
		if key == "AGINEX_HTTP_SHUTDOWN_GRACE_PERIOD" {
			if d, err := time.ParseDuration(value); err == nil && d > grace {
				grace = d + 2*time.Second
			}
		}
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	// Descendants can outlive pnpm; always terminate the owned process group.
	_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
	<-done
}
func privateControlDirectory(path string, create bool) error {
	if create {
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return errors.New("development control directory must be private and owned by the current user")
	}
	return nil
}
func lockDevControl(directory string) (func(), error) {
	fd, err := unix.Open(filepath.Join(directory, "lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, errors.New("a development supervisor already owns this project")
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}
func deadControlSocket(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && (errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED))
}

func cleanupExitedProcessTree(cmd *exec.Cmd) { _ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
