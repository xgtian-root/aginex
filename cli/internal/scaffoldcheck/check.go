// Package scaffoldcheck supplies backend consumer probes as source fixtures.
// The CLI itself never imports backend packages; probes run in a derived module.
package scaffoldcheck

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/mod/modfile"
)

//go:embed fixtures/*.go.txt
var fixtures embed.FS

// Install adds only release-test source to a disposable generated project.
func Install(serverDirectory string) error {
	directory := filepath.Join(serverDirectory, "scaffoldcheck")
	if err := os.Mkdir(directory, 0o755); err != nil {
		return err
	}
	entries, err := fixtures.ReadDir("fixtures")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		content, err := fixtures.ReadFile("fixtures/" + entry.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, strings.TrimSuffix(entry.Name(), ".txt")), content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ValidateDependency rejects hidden local replacements and mismatched pins.
// localBackend is nonempty only for explicitly local development smoke tests.
func ValidateDependency(raw []byte, backend, version, localBackend string) error {
	document, err := modfile.Parse("go.mod", raw, nil)
	if err != nil {
		return err
	}
	matched := false
	for _, dependency := range document.Require {
		if dependency.Mod.Path == backend {
			matched = dependency.Mod.Version == version
		}
	}
	if !matched {
		return errors.New("scaffold backend version mismatch")
	}
	if localBackend == "" {
		if len(document.Replace) != 0 {
			return errors.New("downloaded scaffold verification forbids go.mod replacements")
		}
		return nil
	}
	canonical, err := filepath.EvalSymlinks(localBackend)
	if err != nil {
		return err
	}
	if len(document.Replace) != 1 || document.Replace[0].Old.Path != backend ||
		document.Replace[0].Old.Version != "" || document.Replace[0].New.Version != "" ||
		document.Replace[0].New.Path != filepath.ToSlash(canonical) {
		return errors.New("local scaffold must replace only the selected backend checkout")
	}
	return nil
}

// CompareOpenAPI verifies the downloaded backend against the scaffold contract
// consumed by its generated admin client. Whitespace and key order are ignored.
func CompareOpenAPI(expected, actual []byte) error {
	decode := func(raw []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var document any
		err := decoder.Decode(&document)
		return document, err
	}
	want, err := decode(expected)
	if err != nil {
		return fmt.Errorf("decode scaffold OpenAPI: %w", err)
	}
	got, err := decode(actual)
	if err != nil {
		return fmt.Errorf("decode backend OpenAPI: %w", err)
	}
	if !reflect.DeepEqual(want, got) {
		return errors.New("pinned backend OpenAPI differs from the scaffold admin contract; update the backend pin and regenerate canonical contracts before releasing")
	}
	return nil
}
