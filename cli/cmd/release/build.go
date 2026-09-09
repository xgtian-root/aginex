package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xgtian-root/aginex/cli/internal/buildinfo"
	assets "github.com/xgtian-root/aginex/cli/templates"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type platform struct{ OS, Arch string }

var platforms = []platform{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}}

type artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Version        string     `json:"version"`
	Tag            string     `json:"tag"`
	Commit         string     `json:"commit"`
	BuildDate      string     `json:"buildDate"`
	BackendVersion string     `json:"backendVersion"`
	Artifacts      []artifact `json:"artifacts"`
}

func hostEnv() map[string]string {
	return map[string]string{"GOWORK": "off", "GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0", "GOFLAGS": ""}
}

func validateVersion(version string) error {
	// No major-path migration or Go-incompatible build metadata is implicit.
	if !semver.IsValid(version) || semver.Canonical(version) != version || semver.Major(version) != "v0" && semver.Major(version) != "v1" {
		return fmt.Errorf("expected canonical v0.x.y or v1.x.y version (optional prerelease), got %q", version)
	}
	return nil
}

func validateBackend(cliVersion, backendVersion string) error {
	if err := validateVersion(backendVersion); err != nil {
		return fmt.Errorf("bundled backend: %w", err)
	}
	if strings.Contains(semver.Prerelease(backendVersion), "dev") {
		return fmt.Errorf("backend %s is not pinned; set templates.BackendVersion to a downloadable source-commit pseudo-version", backendVersion)
	}
	if semver.Prerelease(cliVersion) == "" && semver.Prerelease(backendVersion) != "" && !module.IsPseudoVersion(backendVersion) {
		return errors.New("a stable CLI requires a pinned backend source commit or a stable module version")
	}
	return nil
}

func releaseCommit(root, version string) (string, error) {
	if err := validateVersion(version); err != nil {
		return "", err
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	tag, err := git(root, "rev-parse", "refs/tags/cli/"+version+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("release requires the existing cli/%s tag: %w", version, err)
	}
	if tag != head {
		return "", errors.New("release tag does not point at HEAD")
	}
	status, err := git(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", errors.New("release requires a clean worktree; commit reviewed source changes first")
	}
	return head, nil
}

func build(o options) error {
	m := manifest{Version: "v" + buildinfo.DevelopmentVersion, Commit: "unknown", BuildDate: time.Unix(0, 0).UTC().Format(time.RFC3339), BackendVersion: "v" + strings.TrimPrefix(assets.BackendVersion, "v")}
	if o.release != "" {
		if err := validateVersion(o.release); err != nil {
			return err
		}
		commit, err := releaseCommit(o.root, o.release)
		if err != nil {
			return err
		}
		m.Version, m.Tag, m.Commit, o.all = o.release, "cli/"+o.release, commit, true
		if err := validateBackend(m.Version, m.BackendVersion); err != nil {
			return err
		}
	} else if commit, err := git(o.root, "rev-parse", "HEAD"); err == nil {
		m.Commit = commit
		if status, err := git(o.root, "status", "--porcelain"); err != nil {
			return err
		} else if status != "" {
			m.Commit += "-dirty"
		}
	}
	if stamp, err := git(o.root, "show", "-s", "--format=%cI", "HEAD"); err == nil {
		parsed, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			return err
		}
		m.BuildDate = parsed.UTC().Format(time.RFC3339)
	}
	cliDir := filepath.Join(o.root, "cli")
	if _, err := command(cliDir, hostEnv(), nil, "go", "run", "./cmd/sync-templates", "-root", o.root, "-check"); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "aginex-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if o.release != "" {
		if _, err := command(stage, hostEnv(), nil, "go", "mod", "download", "-json", backendModule+"@"+m.BackendVersion); err != nil {
			return fmt.Errorf("pinned framework source %s cannot be downloaded; push its source commit before releasing the CLI: %w", m.BackendVersion, err)
		}
	}
	targets := []platform{{runtime.GOOS, runtime.GOARCH}}
	if o.all {
		targets = platforms
	}
	for _, p := range targets {
		binaryName := "aginex"
		if p.OS == "windows" {
			binaryName += ".exe"
		}
		binary := filepath.Join(stage, p.OS+"_"+p.Arch, binaryName)
		env := hostEnv()
		env["GOOS"], env["GOARCH"] = p.OS, p.Arch
		env["GOAMD64"], env["GOARM64"], env["GOEXPERIMENT"] = "v1", "v8.0", ""
		flags := "-s -w -X " + cliModule + "/internal/buildinfo.Version=" + m.Version + " -X " + cliModule + "/internal/buildinfo.Commit=" + m.Commit + " -X " + cliModule + "/internal/buildinfo.BuildDate=" + m.BuildDate
		fmt.Printf("Building %s/%s %s\n", p.OS, p.Arch, m.Version)
		if _, err := command(cliDir, env, nil, "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=auto", "-ldflags", flags, "-o", binary, "./cmd/aginex"); err != nil {
			return err
		}
		if !o.all {
			target := filepath.Join(o.dist, binaryName)
			if err := copyFile(binary, target, 0o755); err != nil {
				return err
			}
			fmt.Println("Executable:", target)
			return nil
		}
		a, err := pack(stage, o.root, m, p, binary)
		if err != nil {
			return err
		}
		m.Artifacts = append(m.Artifacts, a)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "release.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "SHA256SUMS"), checksums(m), 0o644); err != nil {
		return err
	}
	if m.Tag != "" && semver.Prerelease(m.Version) == "" {
		if err = os.WriteFile(filepath.Join(stage, "aginex.rb"), formula(m), 0o644); err != nil {
			return err
		}
	}
	if _, err = verifyBundle(stage); err != nil {
		return err
	}
	for _, name := range bundleFiles(m) {
		if err = copyFile(filepath.Join(stage, name), filepath.Join(o.dist, name), 0o644); err != nil {
			return err
		}
	}
	fmt.Println("Artifacts:", o.dist)
	return nil
}

func copyFile(source, target string, mode os.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err = os.WriteFile(target, data, mode); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}
