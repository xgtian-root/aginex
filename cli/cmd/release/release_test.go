package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testManifest(version string) manifest {
	m := manifest{Version: version, Tag: "cli/" + version, Commit: strings.Repeat("a", 40), BuildDate: "2026-09-09T00:00:00Z", BackendVersion: "v0.1.0"}
	for _, p := range platforms {
		m.Artifacts = append(m.Artifacts, artifact{OS: p.OS, Arch: p.Arch, Name: archiveName(version, p), SHA256: strings.Repeat("a", 64)})
	}
	return m
}

func TestReleaseValidation(t *testing.T) {
	for _, version := range []string{"v0.1.0", "v0.1.0-dev", "v1.2.3", "v0.2.0-rc.1"} {
		if err := validateVersion(version); err != nil {
			t.Fatal(err)
		}
	}
	for _, version := range []string{"", "1.2.3", "v1.2", "cli/v1.2.3", "v1.2.3+build", "v2.0.0", "v01.2.3", "v1.2.3\n"} {
		if validateVersion(version) == nil {
			t.Errorf("accepted %q", version)
		}
	}
	for _, tc := range []struct {
		cli, backend string
		valid        bool
	}{
		{"v0.1.0", "v0.1.0", true}, {"v0.2.0-rc.1", "v0.2.0-rc.1", true},
		{"v0.1.0-dev", "v0.0.0-20260907062300-aa50cf765344", true},
		{"v0.1.0", "v0.0.0-20260907062300-aa50cf765344", true},
		{"v0.1.0", "v0.1.0-dev", false}, {"v0.2.0", "v0.2.0-rc.1", false},
		{"v0.2.0-rc.1", "v0.1.0-dev", false}, {"v0.1.0", "unknown", false},
	} {
		if (validateBackend(tc.cli, tc.backend) == nil) != tc.valid {
			t.Errorf("backend validation: %+v", tc)
		}
	}
}

func TestReleaseCommitRequiresMatchingTagAndCleanTree(t *testing.T) {
	root := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		out, err := git(root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	runGit("init")
	runGit("config", "user.name", "Release test")
	runGit("config", "user.email", "test@example.invalid")
	runGit("config", "commit.gpgsign", "false")
	writeTestFile(t, filepath.Join(root, "source"), []byte("one"))
	runGit("add", "source")
	runGit("commit", "-m", "test fixture")
	if _, err := releaseCommit(root, "v0.1.0"); err == nil {
		t.Fatal("missing tag accepted")
	}
	runGit("tag", "cli/v0.1.0")
	if _, err := releaseCommit(root, "v0.1.0"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "source"), []byte("two"))
	if _, err := releaseCommit(root, "v0.1.0"); err == nil || !strings.Contains(err.Error(), "clean worktree") {
		t.Fatalf("dirty tree: %v", err)
	}
	runGit("add", "source")
	runGit("commit", "-m", "second fixture")
	if _, err := releaseCommit(root, "v0.1.0"); err == nil || !strings.Contains(err.Error(), "HEAD") {
		t.Fatalf("tag mismatch: %v", err)
	}
}

func TestArchivesAreDeterministicAndExecutable(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "LICENSE"), []byte("license"))
	writeTestFile(t, filepath.Join(root, "NOTICE"), []byte("notice"))
	m := testManifest("v0.1.0")
	for _, p := range platforms {
		t.Run(p.OS+"_"+p.Arch, func(t *testing.T) {
			name := "aginex"
			if p.OS == "windows" {
				name += ".exe"
			}
			binary := filepath.Join(root, p.OS, name)
			data := []byte("test binary")
			writeTestFile(t, binary, data)
			first := t.TempDir()
			second := t.TempDir()
			a, err := pack(first, root, m, p, binary)
			if err != nil {
				t.Fatal(err)
			}
			b, err := pack(second, root, m, p, binary)
			if err != nil {
				t.Fatal(err)
			}
			if a != b {
				t.Fatal("archive metadata is not deterministic")
			}
			raw, err := os.ReadFile(filepath.Join(first, a.Name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := archiveBinary(raw, p.OS)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Fatal("archive binary differs")
			}
		})
	}
}

func TestArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../aginex.exe", "LICENSE", "extra"} {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, entry := range []string{"aginex.exe", "LICENSE", "NOTICE", name} {
			h := &zip.FileHeader{Name: entry}
			h.SetMode(0o755)
			writer, err := w.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			writer.Write([]byte("bytes"))
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := archiveBinary(b.Bytes(), "windows"); err == nil {
			t.Errorf("accepted extra entry %q", name)
		}
	}
}

func TestManifestRejectsIncompleteOrUnsafeBundles(t *testing.T) {
	for _, mutate := range []func(*manifest){
		func(m *manifest) { m.Tag = "cli/v0.2.0" },
		func(m *manifest) { m.Artifacts = m.Artifacts[:5] },
		func(m *manifest) { m.Artifacts[0].Name = "../payload" },
		func(m *manifest) { m.Artifacts[0].SHA256 = "bad" },
		func(m *manifest) { m.Artifacts[1] = m.Artifacts[0] },
		func(m *manifest) { m.BackendVersion = "v0.1.0-dev" },
	} {
		m := testManifest("v0.1.0")
		mutate(&m)
		if err := validateManifest(m); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	if err := validateManifest(testManifest("v0.1.0")); err != nil {
		t.Fatal(err)
	}
}

func TestBundleRejectsChangedArtifact(t *testing.T) {
	m := testManifest("v0.1.0")
	dir := t.TempDir()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "release.json"), raw)
	writeTestFile(t, filepath.Join(dir, m.Artifacts[0].Name), []byte("changed artifact"))
	if _, err = verifyBundle(dir); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered artifact: %v", err)
	}
}

func TestTapPreservesEditsAndNeverDowngrades(t *testing.T) {
	old := testManifest("v0.1.0")
	next := testManifest("v0.2.0")
	if change, err := reconcileFormula(formula(old), old, next); err != nil || !change {
		t.Fatalf("upgrade: %t %v", change, err)
	}
	if change, err := reconcileFormula(formula(next), next, next); err != nil || change {
		t.Fatalf("same version: %t %v", change, err)
	}
	if change, err := reconcileFormula(formula(next), next, old); err != nil || change {
		t.Fatalf("downgrade: %t %v", change, err)
	}
	changed := append(formula(old), []byte("# manual customization\n")...)
	if _, err := reconcileFormula(changed, old, next); err == nil {
		t.Fatal("manual edit overwritten")
	}
	if _, err := reconcileFormula([]byte("class Aginex < Formula\nend\n"), old, next); err == nil {
		t.Fatal("unmanaged formula overwritten")
	}
	if !bytes.Contains(formula(next), []byte("cli%2Fv0.2.0")) {
		t.Fatal("nested release tag is not URL encoded")
	}
	if bytes.Count(formula(next), []byte("sha256 ")) != 4 {
		t.Fatal("missing platform checksums")
	}
}

func TestFormulaRubySyntax(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("Ruby is unavailable; CI also validates with Homebrew")
	}
	path := filepath.Join(t.TempDir(), "aginex.rb")
	writeTestFile(t, path, formula(testManifest("v0.1.0")))
	if _, err = command("", nil, nil, ruby, "-c", path); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRetriesWithoutOverwritingAssets(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		draft, existing, changed bool
		wantError                string
	}{
		{name: "new draft", draft: true},
		{name: "published identical", existing: true},
		{name: "draft conflict", draft: true, existing: true, changed: true, wantError: "different content"},
		{name: "published incomplete", wantError: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManifest("v0.1.0")
			dir := t.TempDir()
			for _, name := range bundleFiles(m) {
				writeTestFile(t, filepath.Join(dir, name), []byte(name))
			}
			previous := github
			t.Cleanup(func() { github = previous })
			writes := 0
			github = func(input []byte, args ...string) ([]byte, error) {
				switch {
				case args[0] == "api" && args[1] == releaseEndpoint(m.Tag):
					remote := githubRelease{Draft: tc.draft, Tag: m.Tag}
					if tc.existing {
						for _, name := range bundleFiles(m) {
							remote.Assets = append(remote.Assets, struct {
								Name string `json:"name"`
							}{name})
						}
					}
					return json.Marshal(remote)
				case args[0] == "api" && strings.HasSuffix(args[1], "/latest"):
					return nil, errors.New("Not Found (HTTP 404)")
				case args[0] == "release" && args[1] == "download":
					name, downloadDir := args[6], args[8]
					data := []byte(name)
					if tc.changed {
						data = []byte("conflict")
					}
					return nil, os.WriteFile(filepath.Join(downloadDir, name), data, 0o644)
				case args[0] == "release" && (args[1] == "upload" || args[1] == "edit"):
					writes++
					return nil, nil
				default:
					t.Fatalf("unexpected GitHub operation: %v", args)
					return nil, nil
				}
			}
			err := publishFiles(dir, m)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got %v; want %s", err, tc.wantError)
				}
				if writes != 0 {
					t.Fatal("conflicting release was mutated")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.name == "published identical" && writes != 0 {
				t.Fatal("published release was mutated")
			}
			if tc.name == "new draft" && writes != len(bundleFiles(m))+1 {
				t.Fatalf("incomplete publication: %d writes", writes)
			}
		})
	}
}

func TestRemoteTagMustAlreadyExistAndMatch(t *testing.T) {
	m := testManifest("v0.1.0")
	for _, tc := range []struct {
		name                         string
		missing, mismatch, annotated bool
	}{
		{name: "lightweight"}, {name: "annotated", annotated: true}, {name: "missing", missing: true}, {name: "different commit", mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := github
			t.Cleanup(func() { github = previous })
			calls := 0
			github = func(_ []byte, args ...string) ([]byte, error) {
				calls++
				if tc.missing {
					return nil, errors.New("Not Found (HTTP 404)")
				}
				kind, sha := "commit", m.Commit
				if tc.annotated && calls == 1 {
					kind, sha = "tag", strings.Repeat("b", 40)
				}
				if tc.mismatch {
					sha = strings.Repeat("c", 40)
				}
				return json.Marshal(map[string]any{"object": map[string]string{"type": kind, "sha": sha}})
			}
			err := verifyRemoteTag(m)
			if (err != nil) != (tc.missing || tc.mismatch) {
				t.Fatalf("unexpected result: %v", err)
			}
			if tc.annotated && calls != 2 {
				t.Fatal("annotated tag not resolved")
			}
		})
	}
}

func TestBuildRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"build", "--not-a-flag"}, {"build", "extra"}, {"build", "--release", "not-a-version"}, {"build", "--release", ""}} {
		if err := run(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
