//go:build !darwin && !linux

package config

import "os"

func publishInstallation(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	// Publication has succeeded. A failed cleanup must not turn this into an
	// apparent uncommitted error; the caller's deferred cleanup retries it.
	_ = os.Remove(source)
	return nil
}
