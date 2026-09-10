package config

import (
	"fmt"
	"github.com/xgtian-root/aginex/server/framework/envfile"
	"os"
	"path/filepath"
	"strings"
)

// ServerEnvPath supports execution from either the project or backend directory.
// Relative data/config paths retain the process working-directory semantics.
func ServerEnvPath() string {
	if st, err := os.Stat(filepath.Join("server", "go.mod")); err == nil && st.Mode().IsRegular() {
		return filepath.Join("server", ".env")
	}
	return ".env"
}
func RuntimeEnvironment() (map[string]string, error) {
	envPath := ServerEnvPath()
	root := "."
	if envPath == ".env" {
		if cwd, e := os.Getwd(); e == nil && filepath.Base(cwd) == "server" {
			root = ".."
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".cache", "aginex", "config-transaction.json")); err == nil {
		return nil, fmt.Errorf("pending configuration transaction; run aginex config recover before startup")
	}
	values, err := envfile.Read(envPath)
	if err != nil {
		return nil, err
	}
	return envfile.Merge(values, envfile.Environment()), nil
}
func InstallationPath(env map[string]string) string {
	if p := strings.TrimSpace(env["AGINEX_CONFIG_FILE"]); p != "" {
		return p
	}
	return DefaultConfigFile
}
