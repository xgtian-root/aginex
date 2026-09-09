package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// Exercise the actual Go module zip/install boundary without publishing a tag
// or contaminating the normal module cache with a fabricated Aginex version.
func TestStandaloneGoInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("standalone module installation")
	}
	cliDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	source := filepath.Join(temp, "module")
	err = filepath.WalkDir(cliDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(cliDir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel == "dist" || strings.HasPrefix(entry.Name(), ".build-") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyFile(path, filepath.Join(source, rel), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	version := "v0.9.99"
	proxyRoot := filepath.Join(temp, "proxy")
	versionDir := filepath.Join(proxyRoot, filepath.FromSlash(cliModule), "@v")
	if err = os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archive, err := os.Create(filepath.Join(versionDir, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	err = modzip.CreateFromDir(archive, module.Version{Path: cliModule, Version: version}, source)
	closeErr := archive.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err = copyFile(filepath.Join(source, "go.mod"), filepath.Join(versionDir, version+".mod"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(versionDir, version+".info"), []byte(fmt.Sprintf(`{"Version":%q,"Time":"2026-09-09T00:00:00Z"}`, version)))
	writeTestFile(t, filepath.Join(versionDir, "list"), []byte(version+"\n"))
	cache, err := command(cliDir, hostEnv(), nil, "go", "env", "GOMODCACHE")
	if err != nil {
		t.Fatal(err)
	}
	fileURL := func(path string) string {
		path = filepath.ToSlash(path)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return (&url.URL{Scheme: "file", Path: path}).String()
	}
	env := hostEnv()
	env["GOBIN"] = filepath.Join(temp, "bin")
	env["GOMODCACHE"] = filepath.Join(temp, "cache")
	env["GOPROXY"] = fileURL(proxyRoot) + "," + fileURL(filepath.Join(strings.TrimSpace(string(cache)), "cache", "download"))
	env["GOSUMDB"] = "off"
	env["GONOPROXY"] = "none"
	env["GOFLAGS"] = "-modcacherw"
	if _, err = command(temp, env, nil, "go", "install", cliModule+"/cmd/aginex@"+version); err != nil {
		t.Fatal(err)
	}
	name := "aginex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(env["GOBIN"], name)
	out, err := command(temp, nil, nil, binary, "--version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "version "+version+"\n") {
		t.Fatalf("installed version: %s", out)
	}
	// A downloaded CLI must render every asset using its pinned framework
	// dependency without requiring a local Aginex checkout.
	if _, err = command(temp, nil, nil, binary, "new", "from-module"); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"AGENTS.md", ".agents/skills/create-aginex-project/SKILL.md", "admin/package.json", "server/go.mod"} {
		if _, err = os.Stat(filepath.Join(temp, "from-module", filepath.FromSlash(file))); err != nil {
			t.Fatal(err)
		}
	}
}
