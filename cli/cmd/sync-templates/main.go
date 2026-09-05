// sync-templates copies an explicit allowlist of canonical source files into the
// standalone CLI. -check is read-only and fails if any source or snapshot drifts.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	check := flag.Bool("check", false, "verify the snapshot without writing")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := sync(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func sync(root string, check bool) error {
	base := filepath.Join(root, "cli", "templates")
	raw, err := os.ReadFile(filepath.Join(base, "sources.json"))
	if err != nil {
		return err
	}
	var sources []string
	if err := json.Unmarshal(raw, &sources); err != nil {
		return err
	}
	old := map[string]string{}
	manifest := filepath.Join(base, "snapshot.json")
	if raw, err := os.ReadFile(manifest); err == nil {
		if err := json.Unmarshal(raw, &old); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	contents := map[string][]byte{}
	hashes := map[string]string{}
	for _, source := range sources {
		if !fs.ValidPath(source) || forbidden(source) {
			return fmt.Errorf("unsafe template source %q", source)
		}
		if _, exists := contents[source]; exists {
			return fmt.Errorf("duplicate template source %q", source)
		}
		name := filepath.Join(root, filepath.FromSlash(source))
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("template source is not a regular file: %s", source)
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		contents[source] = content
		hashes[source] = digest(content)
	}
	project := filepath.Join(base, "_project")
	drift := false
	err = filepath.WalkDir(project, func(name string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) && name == project {
			drift = true
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(project, name)
		if err != nil {
			return err
		}
		relative = strings.TrimSuffix(filepath.ToSlash(relative), ".template")
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular snapshot file %s", relative)
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if old[relative] != digest(content) {
			return fmt.Errorf("snapshot was locally modified: %s; preserve changes and reconcile with its canonical source before syncing", relative)
		}
		if !bytes.Equal(content, contents[relative]) {
			drift = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	for source, content := range contents {
		existing, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(snapshotPath(source))))
		if err != nil || !bytes.Equal(content, existing) {
			drift = true
		}
	}
	encoded, err := json.MarshalIndent(hashes, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	existingManifest, _ := os.ReadFile(manifest)
	if !bytes.Equal(encoded, existingManifest) {
		drift = true
	}
	if check {
		if drift {
			return fmt.Errorf("template snapshot drift; run go run ./cli/cmd/sync-templates")
		}
		return nil
	}
	// All drift checks complete before the first write.
	for source := range old {
		if _, exists := contents[source]; !exists {
			if err := os.Remove(filepath.Join(project, filepath.FromSlash(snapshotPath(source)))); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	slices.Sort(sources)
	for _, source := range sources {
		name := filepath.Join(project, filepath.FromSlash(snapshotPath(source)))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(name, contents[source], 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(manifest, encoded, 0o644)
}

func digest(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }
func forbidden(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if slices.Contains([]string{".git", ".cache", ".next", "node_modules", "data", ".pnpm-store", "test-results", "playwright-report"}, part) {
			return true
		}
		if strings.HasPrefix(part, ".env") && part != ".env.example" {
			return true
		}
	}
	return false
}

func snapshotPath(source string) string {
	if filepath.Base(source) == "go.mod" {
		return source + ".template"
	}
	return source
}
