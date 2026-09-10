//go:build !unix

package cli

import (
	"errors"
	"os/exec"
)

func configureProcessTree(cmd *exec.Cmd)                  {}
func stopProcessTree(cmd *exec.Cmd, done <-chan struct{}) { _ = cmd.Process.Kill(); <-done }
func privateControlDirectory(string, bool) error {
	return errors.New("managed development configuration currently requires a Unix host")
}
func lockDevControl(string) (func(), error) {
	return nil, errors.New("managed development configuration currently requires a Unix host")
}
func deadControlSocket(error) bool { return false }

func cleanupExitedProcessTree(cmd *exec.Cmd) {}
