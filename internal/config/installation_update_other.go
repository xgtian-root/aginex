//go:build !unix

package config

func lockInstallationUpdate(string) (func(), error) {
	return func() {}, nil
}
