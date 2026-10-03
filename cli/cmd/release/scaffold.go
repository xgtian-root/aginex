package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xgtian-root/aginex/cli/internal/scaffoldcheck"
)

// verifyScaffold runs against the generated module. Release bundles must use
// their real downloadable pin; only development bundles may use a local checkout.
func verifyScaffold(project string, m manifest, root string) error {
	server := filepath.Join(project, "server")
	goMod, err := os.ReadFile(filepath.Join(server, "go.mod"))
	if err != nil {
		return err
	}
	localBackend := ""
	if m.Tag == "" {
		localBackend = filepath.Join(root, "server")
	}
	if err := scaffoldcheck.ValidateDependency(goMod, backendModule, m.BackendVersion, localBackend); err != nil {
		return err
	}
	env := hostEnv()
	for _, args := range [][]string{
		{"mod", "download", "all"},
		{"build", "-mod=readonly", "./..."},
		{"run", "-mod=readonly", "./cmd/openapi", "-output", filepath.Join(project, "backend-openapi.json")},
	} {
		if _, err := command(server, env, nil, "go", args...); err != nil {
			return fmt.Errorf("verify scaffold backend %s: %w", m.BackendVersion, err)
		}
	}
	expected, err := os.ReadFile(filepath.Join(project, "docs", "openapi.json"))
	if err != nil {
		return err
	}
	actual, err := os.ReadFile(filepath.Join(project, "backend-openapi.json"))
	if err != nil {
		return err
	}
	if err := scaffoldcheck.CompareOpenAPI(expected, actual); err != nil {
		return err
	}
	if err := scaffoldcheck.Install(server); err != nil {
		return err
	}
	// The fixture adds test imports, so normalize only this disposable module
	// before enforcing readonly dependencies for the actual consumer tests.
	if _, err := command(server, env, nil, "go", "mod", "tidy"); err != nil {
		return err
	}
	if _, err := command(server, env, nil, "go", "test", "-mod=readonly", "-count=1", "./scaffoldcheck"); err != nil {
		return fmt.Errorf("scaffold login and public file contracts are incompatible with backend %s: %w", m.BackendVersion, err)
	}
	if m.Tag == "" {
		fmt.Println("Scaffold contracts passed with a local checkout; downloadable backend compatibility remains unverified.")
	} else {
		fmt.Printf("Downloaded scaffold contracts passed: %s (GOWORK=off, no replace).\n", m.BackendVersion)
	}
	return nil
}
