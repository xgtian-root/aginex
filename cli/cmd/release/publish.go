package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

var github = func(input []byte, args ...string) ([]byte, error) { return command("", nil, input, "gh", args...) }

func notFound(err error) bool { return err != nil && strings.Contains(err.Error(), "(HTTP 404)") }

type githubRelease struct {
	Draft  bool   `json:"draft"`
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func releaseEndpoint(tag string) string {
	return "repos/" + repository + "/releases/tags/" + url.PathEscape(tag)
}

func publish(o options) error {
	m, err := verifyBundle(o.dist)
	if err != nil {
		return err
	}
	if m.Tag == "" {
		return errors.New("development builds cannot be published")
	}
	commit, err := releaseCommit(o.root, m.Version)
	if err != nil {
		return err
	}
	if commit != m.Commit {
		return errors.New("artifact commit does not match source/tag")
	}
	if err = verifyRemoteTag(m); err != nil {
		return err
	}
	return publishFiles(o.dist, m)
}

func verifyRemoteTag(m manifest) error {
	endpoint := "repos/" + repository + "/git/ref/tags/" + url.PathEscape(m.Tag)
	for depth := 0; depth < 8; depth++ {
		raw, err := github(nil, "api", endpoint)
		if err != nil {
			return fmt.Errorf("release requires the existing remote tag %s: %w", m.Tag, err)
		}
		var result struct {
			Object struct {
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"object"`
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return err
		}
		switch result.Object.Type {
		case "commit":
			if result.Object.SHA != m.Commit {
				return errors.New("remote tag commit differs from release artifacts")
			}
			return nil
		case "tag":
			endpoint = "repos/" + repository + "/git/tags/" + url.PathEscape(result.Object.SHA)
		default:
			return errors.New("remote release tag does not resolve to a commit")
		}
	}
	return errors.New("remote annotated tag chain is too deep")
}

func publishFiles(dir string, m manifest) error {
	raw, err := github(nil, "api", releaseEndpoint(m.Tag))
	var remote githubRelease
	if notFound(err) {
		body, _ := json.Marshal(map[string]any{
			"tag_name": m.Tag, "target_commitish": m.Commit,
			"name": "Aginex CLI " + m.Version, "body": releaseNotes(m),
			"draft": true, "prerelease": semver.Prerelease(m.Version) != "",
		})
		raw, err = github(body, "api", "repos/"+repository+"/releases", "--method", "POST", "--input", "-")
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &remote); err != nil {
		return err
	}
	if remote.Tag != m.Tag {
		return errors.New("remote release tag mismatch")
	}
	downloads, err := os.MkdirTemp("", "aginex-release-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(downloads)
	existing := map[string]bool{}
	for _, a := range remote.Assets {
		existing[a.Name] = true
	}
	// Validate ALL existing assets before uploading any missing ones. A retry
	// must not partially mutate a release that was built from different bytes.
	var missing []string
	for _, name := range bundleFiles(m) {
		if !existing[name] {
			if !remote.Draft {
				return fmt.Errorf("published release is missing %s; refusing to modify it", name)
			}
			missing = append(missing, name)
			continue
		}
		if _, err = github(nil, "release", "download", m.Tag, "--repo", repository, "--pattern", name, "--dir", downloads); err != nil {
			return err
		}
		actual, err := os.ReadFile(filepath.Join(downloads, name))
		if err != nil {
			return err
		}
		want, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, want) {
			return fmt.Errorf("existing release asset %s has different content; refusing to overwrite", name)
		}
	}
	for _, name := range missing {
		if _, err = github(nil, "release", "upload", m.Tag, filepath.Join(dir, name), "--repo", repository); err != nil {
			return err
		}
	}
	if remote.Draft {
		latest := false
		if semver.Prerelease(m.Version) == "" {
			latest = true
			raw, err := github(nil, "api", "repos/"+repository+"/releases/latest")
			if err != nil && !notFound(err) {
				return err
			}
			if err == nil {
				var current githubRelease
				if err = json.Unmarshal(raw, &current); err != nil {
					return err
				}
				version := strings.TrimPrefix(current.Tag, "cli/")
				if semver.IsValid(version) && semver.Compare(version, m.Version) > 0 {
					latest = false
				}
			}
		}
		_, err = github(nil, "release", "edit", m.Tag, "--repo", repository, "--draft=false", fmt.Sprintf("--latest=%t", latest))
		if err != nil {
			return err
		}
	}
	fmt.Println("Published and verified:", m.Tag)
	return nil
}

func releaseNotes(m manifest) string {
	notes := fmt.Sprintf("Aginex CLI %s\n\nInstall with Go:\n\n```bash\ngo install %s/cmd/aginex@%s\n```\n\nBinary archives are available for macOS, Linux, and Windows on amd64 and arm64. Verify downloads with `SHA256SUMS`.\n\nGenerated projects pin the framework source to `%s`. No separate server release is required.\n\nSource commit: `%s`.\n", m.Version, cliModule, m.Version, m.BackendVersion, m.Commit)
	if semver.Prerelease(m.Version) != "" {
		return notes + "\nThis is a development prerelease; it does not update the default Homebrew formula.\n"
	}
	return notes + "\nAfter the Homebrew publishing job succeeds:\n\n```bash\nbrew install xgtian-root/tap/aginex\n```\n"
}

func formula(m manifest) []byte {
	var b strings.Builder
	b.WriteString("# Generated by Aginex CLI release tooling. Manual edits are detected before updates.\nclass Aginex < Formula\n")
	fmt.Fprintf(&b, "  desc \"Go and Next.js application scaffolding and developer tools\"\n  homepage \"https://github.com/%s\"\n  version %q\n  license \"Apache-2.0\"\n\n", repository, strings.TrimPrefix(m.Version, "v"))
	for _, osPair := range [][2]string{{"darwin", "macos"}, {"linux", "linux"}} {
		fmt.Fprintf(&b, "  on_%s do\n", osPair[1])
		for _, a := range m.Artifacts {
			if a.OS != osPair[0] {
				continue
			}
			arch := "intel"
			if a.Arch == "arm64" {
				arch = "arm"
			}
			fmt.Fprintf(&b, "    on_%s do\n      url \"https://github.com/%s/releases/download/%s/%s\"\n      sha256 %q\n    end\n", arch, repository, url.PathEscape(m.Tag), a.Name, a.SHA256)
		}
		b.WriteString("  end\n\n")
	}
	b.WriteString("  def install\n    bin.install \"aginex\"\n  end\n\n  test do\n    assert_match version.to_s, shell_output(\"#{bin}/aginex --version\")\n    assert_match \"Usage:\", shell_output(\"#{bin}/aginex --help\")\n  end\nend\n")
	return []byte(b.String())
}

func downloadManifest(version, dir string) (manifest, error) {
	var m manifest
	if err := validateVersion(version); err != nil {
		return m, err
	}
	raw, err := github(nil, "api", releaseEndpoint("cli/"+version))
	if err != nil {
		return m, err
	}
	var release githubRelease
	if err = json.Unmarshal(raw, &release); err != nil {
		return m, err
	}
	if release.Draft || release.Tag != "cli/"+version {
		return m, errors.New("tap requires a published release with the matching tag")
	}
	if _, err = github(nil, "release", "download", "cli/"+version, "--repo", repository, "--pattern", "release.json", "--dir", dir); err != nil {
		return m, err
	}
	m, err = readManifest(filepath.Join(dir, "release.json"))
	if err != nil {
		return m, err
	}
	if m.Version != version || m.Tag != "cli/"+version {
		return m, errors.New("downloaded manifest version mismatch")
	}
	return m, nil
}

var formulaVersion = regexp.MustCompile(`(?m)^  version "([^"]+)"$`)

func currentFormulaVersion(content []byte) (string, error) {
	if !bytes.HasPrefix(content, []byte("# Generated by Aginex CLI release tooling.")) {
		return "", errors.New("existing tap formula is not managed by this release tool; reconcile manually")
	}
	match := formulaVersion.FindSubmatch(content)
	if len(match) != 2 {
		return "", errors.New("cannot read existing formula version")
	}
	version := "v" + string(match[1])
	return version, validateVersion(version)
}

func reconcileFormula(current []byte, previous, next manifest) (bool, error) {
	version, err := currentFormulaVersion(current)
	if err != nil {
		return false, err
	}
	if semver.Compare(version, next.Version) > 0 {
		return false, nil
	}
	if version != previous.Version || !bytes.Equal(current, formula(previous)) {
		return false, errors.New("tap formula has drifted from its published release; preserve and reconcile manual changes")
	}
	return !bytes.Equal(current, formula(next)), nil
}

func updateTap(version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if semver.Prerelease(version) != "" {
		fmt.Println("Prerelease: default Homebrew formula is unchanged")
		return nil
	}
	dir, err := os.MkdirTemp("", "aginex-tap-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	next, err := downloadManifest(version, dir)
	if err != nil {
		return err
	}
	endpoint := "repos/" + tapRepository + "/contents/Formula/aginex.rb"
	raw, err := github(nil, "api", endpoint)
	var current struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err != nil && !notFound(err) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &current); err != nil {
			return err
		}
		content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(current.Content, "\n", ""))
		if err != nil {
			return err
		}
		oldVersion, err := currentFormulaVersion(content)
		if err != nil {
			return err
		}
		if semver.Compare(oldVersion, version) > 0 {
			fmt.Println("Tap already has a newer version:", oldVersion)
			return nil
		}
		previous := next
		if oldVersion != version {
			previousDir := filepath.Join(dir, "previous")
			if err = os.Mkdir(previousDir, 0o755); err != nil {
				return err
			}
			previous, err = downloadManifest(oldVersion, previousDir)
			if err != nil {
				return err
			}
		}
		change, err := reconcileFormula(content, previous, next)
		if err != nil {
			return err
		}
		if !change {
			fmt.Println("Tap already matches", version)
			return nil
		}
	}
	body := map[string]string{"message": "chore: update aginex to " + version, "content": base64.StdEncoding.EncodeToString(formula(next))}
	if current.SHA != "" {
		body["sha"] = current.SHA
	}
	input, _ := json.Marshal(body)
	// The Contents API performs a compare-and-swap using SHA; concurrent manual
	// edits fail instead of being force-pushed away.
	if _, err = github(input, "api", endpoint, "--method", "PUT", "--input", "-"); err != nil {
		return err
	}
	fmt.Println("Updated", tapRepository, "to", version)
	return nil
}

func verifyInstall(version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "aginex-go-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	env := hostEnv()
	env["GOBIN"] = dir
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		_, last = command(dir, env, nil, "go", "install", cliModule+"/cmd/aginex@"+version)
		if last == nil {
			break
		}
		if attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 10 * time.Second)
		}
	}
	if last != nil {
		return fmt.Errorf("Go installation verification failed (proxy propagation may require a retry): %w", last)
	}
	name := "aginex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out, err := command(dir, nil, nil, filepath.Join(dir, name), "--version")
	if err != nil {
		return err
	}
	// Match the complete version token, so v1.2.3 cannot accidentally accept v1.2.30.
	if !strings.Contains(" "+strings.TrimSpace(string(out))+" ", " "+version+" ") {
		return fmt.Errorf("go install reports unexpected version: %s", out)
	}
	if _, err = command(dir, nil, nil, filepath.Join(dir, name), "new", "installed-project"); err != nil {
		return err
	}
	projectDir := filepath.Join(dir, "installed-project", "server")
	if _, err = command(projectDir, hostEnv(), nil, "go", "mod", "download"); err != nil {
		return err
	}
	if _, err = command(projectDir, hostEnv(), nil, "go", "build", "-mod=readonly", "./..."); err != nil {
		return err
	}
	fmt.Println("Verified go install:", version)
	return nil
}
