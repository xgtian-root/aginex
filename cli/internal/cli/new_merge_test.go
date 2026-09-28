package cli

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeMergeFile(t *testing.T, root, name, content string) {
	t.Helper()
	destination := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewCommandRejectsReservedPaths(t *testing.T) {
	for _, name := range []string{"admin", "server"} {
		for _, kind := range []string{"directory", "file", "symlink", "dangling symlink"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				target := t.TempDir()
				path := filepath.Join(target, name)
				var err error
				switch kind {
				case "directory":
					err = os.Mkdir(path, 0o755)
				case "file":
					err = os.WriteFile(path, []byte("keep"), 0o600)
				case "symlink":
					err = os.Symlink(t.TempDir(), path)
				default:
					err = os.Symlink(filepath.Join(target, "missing"), path)
				}
				if err != nil {
					t.Fatal(err)
				}
				before := snapshotTestTree(t, target)
				_, err = executeNewProjectCommand(t, newProjectDependencies{workingDirectory: fixedWorkingDirectory(target), assets: minimalScaffoldAssets(), frameworkVersion: fixedFrameworkVersion(testFrameworkVersion)})
				if err == nil || !strings.Contains(err.Error(), "reserved path") {
					t.Fatalf("error = %v", err)
				}
				if after := snapshotTestTree(t, target); !reflect.DeepEqual(before, after) {
					t.Fatal("rejected initialization mutated target")
				}
			})
		}
	}
}

func TestNewCommandMergesAndBacksUpCompleteScaffold(t *testing.T) {
	target := t.TempDir()
	for name, content := range map[string]string{
		"README.md": "old readme", "package.json": "old package", "docs/openapi.json": "old api", ".aginex/project.json": "old manifest",
		"docs/personal.md": "personal", ".aginex/notes.txt": "notes", ".git/config": "git", ".env": "private", ".agents/custom/SKILL.md": "custom skill",
	} {
		writeMergeFile(t, target, name, content)
	}
	before := snapshotTestTree(t, target)
	var last string
	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(target), frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
		publishPath: func(source, destination string) error { last = destination; return publishNewPath(source, destination) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if last != filepath.Join(target, ".aginex/project.json") {
		t.Fatalf("last publication = %s", last)
	}
	backups, _ := filepath.Glob(filepath.Join(target, ".aginex-backup-*"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if !strings.Contains(output, backups[0]) {
		t.Fatalf("missing backup output: %s", output)
	}
	after := snapshotTestTree(t, target)
	for _, name := range []string{"docs/personal.md", ".aginex/notes.txt", ".git/config", ".env", ".agents/custom/SKILL.md"} {
		if !reflect.DeepEqual(before[name], after[name]) {
			t.Fatalf("unrelated path changed: %s", name)
		}
	}
	backupTree := snapshotTestTree(t, backups[0])
	for _, name := range []string{"README.md", "package.json", "docs/openapi.json", ".aginex/project.json"} {
		if !reflect.DeepEqual(before[name], backupTree[name]) {
			t.Fatalf("incorrect backup: %s", name)
		}
		if !strings.Contains(output, name) {
			t.Fatalf("missing conflict output: %s", name)
		}
	}
	for _, file := range readProjectManifest(t, target).Files {
		if strings.HasPrefix(file.Path, ".aginex-backup-") || file.Path == "docs/personal.md" || file.Path == ".env" {
			t.Fatalf("manifest includes existing file: %s", file.Path)
		}
		content := readTestFile(t, filepath.Join(target, filepath.FromSlash(file.Path)))
		if fmt.Sprintf("%x", sha256.Sum256(content)) != file.SHA256 {
			t.Fatalf("manifest hash mismatch: %s", file.Path)
		}
	}
}

func TestNewCommandBacksUpTypeConflictsAndSymlinks(t *testing.T) {
	for _, kind := range []string{"directory over file", "file over directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			target := t.TempDir()
			external := t.TempDir()
			writeMergeFile(t, external, "keep", "external")
			conflict := "README.md"
			switch kind {
			case "directory over file":
				conflict = ".aginex"
				writeMergeFile(t, target, conflict, "old file")
			case "file over directory":
				writeMergeFile(t, target, "README.md/keep", "old directory")
			case "symlink":
				conflict = ".aginex"
				if err := os.Symlink(external, filepath.Join(target, conflict)); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Symlink(filepath.Join(external, "missing"), filepath.Join(target, conflict)); err != nil {
					t.Fatal(err)
				}
			}
			original, err := fingerprintPath(filepath.Join(target, conflict))
			if err != nil {
				t.Fatal(err)
			}
			externalBefore := snapshotTestTree(t, external)
			_, err = executeNewProjectCommand(t, newProjectDependencies{workingDirectory: fixedWorkingDirectory(target), assets: minimalScaffoldAssets(), frameworkVersion: fixedFrameworkVersion(testFrameworkVersion)})
			if err != nil {
				t.Fatal(err)
			}
			backups, _ := filepath.Glob(filepath.Join(target, ".aginex-backup-*"))
			if len(backups) != 1 {
				t.Fatalf("backups = %v", backups)
			}
			saved, err := fingerprintPath(filepath.Join(backups[0], conflict))
			if err != nil || saved != original {
				t.Fatalf("backup mismatch: %v", err)
			}
			if !reflect.DeepEqual(externalBefore, snapshotTestTree(t, external)) {
				t.Fatal("symlink target changed")
			}
		})
	}
}

func TestNewCommandMergeRollsBackEveryMoveFailure(t *testing.T) {
	// Exercise failure at each backup/publication boundary until a full run succeeds.
	for failAt := 1; failAt < 30; failAt++ {
		target := t.TempDir()
		writeMergeFile(t, target, "README.md", "old readme")
		writeMergeFile(t, target, ".aginex/project.json", "old manifest")
		writeMergeFile(t, target, ".aginex/keep", "keep")
		writeMergeFile(t, target, "asset.txt/keep", "type conflict")
		before := snapshotTestTree(t, target)
		calls := 0
		injected := errors.New("injected move failure")
		output, err := executeNewProjectCommand(t, newProjectDependencies{
			workingDirectory: fixedWorkingDirectory(target), assets: minimalScaffoldAssets(), frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
			publishPath: func(source, destination string) error {
				calls++
				if calls == failAt {
					return injected
				}
				return publishNewPath(source, destination)
			},
		})
		if err == nil {
			return
		}
		if !errors.Is(err, injected) {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, snapshotTestTree(t, target)) {
			t.Fatalf("failure at move %d did not restore original target", failAt)
		}
		if strings.Contains(output, "Created Aginex project") {
			t.Fatal("failure reported success")
		}
	}
	t.Fatal("never reached successful publication")
}

func TestNewCommandMergePreservesConcurrentReplacementAndBackup(t *testing.T) {
	target := t.TempDir()
	writeMergeFile(t, target, "README.md", "original")
	_, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(target), assets: minimalScaffoldAssets(), frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
		publishPath: func(source, destination string) error {
			if destination == filepath.Join(target, "asset.txt") {
				return errors.New("stop")
			}
			if err := publishNewPath(source, destination); err != nil {
				return err
			}
			if destination == filepath.Join(target, "README.md") {
				return os.WriteFile(destination, []byte("concurrent edit"), 0o600)
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "preserve changed published path") || !strings.Contains(err.Error(), "recovery backup:") {
		t.Fatalf("error = %v", err)
	}
	if string(readTestFile(t, filepath.Join(target, "README.md"))) != "concurrent edit" {
		t.Fatal("lost concurrent edit")
	}
	backups, _ := filepath.Glob(filepath.Join(target, ".aginex-backup-*"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if string(readTestFile(t, filepath.Join(backups[0], "README.md"))) != "original" {
		t.Fatal("lost original")
	}
}

func TestNewCommandRechecksReservedPathsBeforePublication(t *testing.T) {
	target := t.TempDir()
	writeMergeFile(t, target, "README.md", "original")
	published := false
	_, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(target), assets: minimalScaffoldAssets(),
		frameworkVersion: func() (string, error) {
			writeMergeFile(t, target, "server/keep", "concurrent")
			return testFrameworkVersion, nil
		},
		publishPath: func(source, destination string) error { published = true; return publishNewPath(source, destination) },
	})
	if err == nil || !strings.Contains(err.Error(), "reserved path") || published {
		t.Fatalf("error=%v, published=%v", err, published)
	}
	if string(readTestFile(t, filepath.Join(target, "README.md"))) != "original" {
		t.Fatal("existing file changed")
	}
	if string(readTestFile(t, filepath.Join(target, "server/keep"))) != "concurrent" {
		t.Fatal("concurrent file changed")
	}
	backups, _ := filepath.Glob(filepath.Join(target, ".aginex-backup-*"))
	if len(backups) != 0 {
		t.Fatalf("unexpected backup: %v", backups)
	}
}

func TestNewCommandPreservesReservedPathAppearingDuringPublication(t *testing.T) {
	target := t.TempDir()
	writeMergeFile(t, target, "README.md", "original")
	_, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(target), assets: minimalScaffoldAssets(), frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
		publishPath: func(source, destination string) error {
			if err := publishNewPath(source, destination); err != nil {
				return err
			}
			if destination == filepath.Join(target, "asset.txt") {
				writeMergeFile(t, target, "server/keep", "concurrent")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "reserved target path") {
		t.Fatalf("error=%v", err)
	}
	if string(readTestFile(t, filepath.Join(target, "README.md"))) != "original" {
		t.Fatal("original was not restored")
	}
	if string(readTestFile(t, filepath.Join(target, "server/keep"))) != "concurrent" {
		t.Fatal("concurrent file changed")
	}
	backups, _ := filepath.Glob(filepath.Join(target, ".aginex-backup-*"))
	if len(backups) != 0 {
		t.Fatalf("unexpected backup: %v", backups)
	}
}
