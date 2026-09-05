package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/mod/modfile"
)

const testFrameworkVersion = "v0.9.0"

func TestNewCommandInitializesCurrentDirectory(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "My Project 42")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(target),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	})
	if err != nil {
		t.Fatal(err)
	}

	goMod := readTestFile(t, filepath.Join(target, "backend/go.mod"))
	assertGeneratedGoMod(t, goMod, "my-project-42", testFrameworkVersion)
	manifest := readProjectManifest(t, target)
	if manifest.ProjectName != "my-project-42" || manifest.ModulePath != "my-project-42" {
		t.Fatalf("manifest project identity = %#v", manifest)
	}
	if !strings.Contains(output, "Created Aginex project my-project-42 in "+target) {
		t.Fatalf("command output = %q", output)
	}
	assertNoProjectStagingDirectories(t, parent)
}

func TestNewCommandCreatesNamedChildDirectory(t *testing.T) {
	workingDirectory := t.TempDir()
	target := filepath.Join(workingDirectory, "test-project")

	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	}, "test-project")
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("named target mode = %v", info.Mode())
	}
	goMod := readTestFile(t, filepath.Join(target, "backend/go.mod"))
	assertGeneratedGoMod(t, goMod, "test-project", testFrameworkVersion)
	if !strings.Contains(output, "Created Aginex project test-project in "+target) {
		t.Fatalf("command output = %q", output)
	}
	assertNoProjectStagingDirectories(t, workingDirectory)
}

func TestNewCommandUsesExplicitGoModulePath(t *testing.T) {
	workingDirectory := t.TempDir()
	target := filepath.Join(workingDirectory, "test-project")

	_, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	}, "--module", "github.com/example/test-project", "test-project")
	if err != nil {
		t.Fatal(err)
	}

	goMod := readTestFile(t, filepath.Join(target, "backend/go.mod"))
	assertGeneratedGoMod(
		t,
		goMod,
		"github.com/example/test-project",
		testFrameworkVersion,
	)
	manifest := readProjectManifest(t, target)
	if manifest.ProjectName != "test-project" ||
		manifest.ModulePath != "github.com/example/test-project" {
		t.Fatalf("manifest project identity = %#v", manifest)
	}
	server := readTestFile(t, filepath.Join(target, "backend", "cmd", "server", "main.go"))
	if !bytes.Contains(
		server,
		[]byte(`"github.com/example/test-project/internal/composition"`),
	) {
		t.Fatalf("server imports = %q", server)
	}
}

func TestNewCommandUsesExplicitLocalAginexCheckout(t *testing.T) {
	workingDirectory := t.TempDir()
	localAginex := filepath.Join(t.TempDir(), "Aginex Source")
	if err := os.MkdirAll(filepath.Join(localAginex, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(localAginex, "backend/go.mod"),
		[]byte("module "+frameworkModulePath+"\n\ngo 1.25.0\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	_, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: func() (string, error) {
			return "", errUnpublishedFrameworkBuild
		},
	}, "--aginex-path", localAginex, "local-app")
	if err != nil {
		t.Fatal(err)
	}

	goModPath := filepath.Join(workingDirectory, "local-app", "backend/go.mod")
	parsed, err := modfile.Parse(goModPath, readTestFile(t, goModPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	canonicalLocalAginex, err := filepath.EvalSymlinks(localAginex)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Replace) != 1 ||
		parsed.Replace[0].Old.Path != frameworkModulePath ||
		parsed.Replace[0].New.Path != filepath.ToSlash(filepath.Join(canonicalLocalAginex, "backend")) {
		t.Fatalf("local framework replacements = %#v", parsed.Replace)
	}
}

func TestNewCommandRejectsUnpublishedSourceBuildWithoutLocalCheckout(t *testing.T) {
	workingDirectory := t.TempDir()
	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: func() (string, error) {
			return "", errUnpublishedFrameworkBuild
		},
	}, "source-app")
	if !errors.Is(err, errUnpublishedFrameworkBuild) ||
		!strings.Contains(err.Error(), "--aginex-path") {
		t.Fatalf("source-build error = %v", err)
	}
	assertTestDirectoryEmpty(t, workingDirectory)
	if strings.Contains(output, "Created Aginex project") {
		t.Fatalf("failure output reported success: %q", output)
	}
}

func TestResolveFrameworkBuildVersion(t *testing.T) {
	tests := []struct {
		name            string
		mainVersion     string
		declaredVersion string
		modified        bool
		want            string
		wantSourceError bool
	}{
		{
			name:        "CLI version cannot supply backend version",
			mainVersion: "v1.2.3", declaredVersion: "0.1.0-dev", wantSourceError: true,
		},
		{
			name:        "independent backend release",
			mainVersion: "v1.3.0", declaredVersion: "1.2.0", want: "v1.2.0",
		},
		{
			name:            "release binary metadata",
			mainVersion:     "(devel)",
			declaredVersion: "1.2.3",
			want:            "v1.2.3",
		},
		{
			name:            "unpublished source build",
			mainVersion:     "(devel)",
			declaredVersion: "0.1.0-dev",
			wantSourceError: true,
		},
		{
			name:            "dirty version marker",
			mainVersion:     "v1.2.3+dirty",
			declaredVersion: "1.2.3",
			wantSourceError: true,
		},
		{
			name:            "modified build setting",
			mainVersion:     "(devel)",
			declaredVersion: "1.2.3",
			modified:        true,
			wantSourceError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveFrameworkBuildVersion(
				test.mainVersion,
				test.declaredVersion,
				test.modified,
			)
			if test.wantSourceError {
				if !errors.Is(err, errUnpublishedFrameworkBuild) {
					t.Fatalf("source-build error = %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("version = %q, error = %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestNewCommandRejectsNonAginexLocalCheckoutWithoutMutation(t *testing.T) {
	workingDirectory := t.TempDir()
	wrongCheckout := t.TempDir()
	if err := os.Mkdir(filepath.Join(wrongCheckout, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(wrongCheckout, "backend/go.mod"),
		[]byte("module example.com/not-aginex\n\ngo 1.25.0\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	}, "--aginex-path", wrongCheckout, "wrong-checkout")
	if err == nil || !strings.Contains(err.Error(), "must declare module "+frameworkModulePath) {
		t.Fatalf("wrong-checkout error = %v", err)
	}
	assertTestDirectoryEmpty(t, workingDirectory)
	if strings.Contains(output, "Created Aginex project") {
		t.Fatalf("failure output reported success: %q", output)
	}
}

func TestNewCommandRejectsInvalidGoModulePathWithoutMutation(t *testing.T) {
	workingDirectory := t.TempDir()
	workingDirectoryCalls := 0

	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: func() (string, error) {
			workingDirectoryCalls++
			return workingDirectory, nil
		},
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	}, "--module", "github.com/example/bad module", "test-project")
	if err == nil || !strings.Contains(err.Error(), "invalid Go module path") {
		t.Fatalf("module-path error = %v", err)
	}
	if workingDirectoryCalls != 0 {
		t.Fatalf("working directory resolved %d times", workingDirectoryCalls)
	}
	assertTestDirectoryEmpty(t, workingDirectory)
	if strings.Contains(output, "Created Aginex project") {
		t.Fatalf("failure output reported success: %q", output)
	}
}

func TestNewCommandRejectsVendorModulePathSegmentsWithoutMutation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "default module", args: []string{"vendor"}},
		{name: "terminal segment", args: []string{"--module", "example.com/acme/vendor", "test-project"}},
		{name: "middle segment", args: []string{"--module", "example.com/vendor/app", "test-project"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workingDirectory := t.TempDir()
			output, err := executeNewProjectCommand(t, newProjectDependencies{
				workingDirectory: fixedWorkingDirectory(workingDirectory),
				assets:           minimalScaffoldAssets(),
				frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
			}, test.args...)
			if err == nil || !strings.Contains(err.Error(), `reserved path segment "vendor"`) {
				t.Fatalf("vendor module error = %v", err)
			}
			assertTestDirectoryEmpty(t, workingDirectory)
			if strings.Contains(output, "Created Aginex project") {
				t.Fatalf("failure output reported success: %q", output)
			}
		})
	}
}

func TestNewCommandRejectsFrameworkModuleCollisionsWithoutMutation(t *testing.T) {
	for _, modulePath := range []string{
		frameworkModulePath,
		"github.com/google/uuid",
	} {
		t.Run(modulePath, func(t *testing.T) {
			workingDirectory := t.TempDir()
			output, err := executeNewProjectCommand(t, newProjectDependencies{
				workingDirectory: fixedWorkingDirectory(workingDirectory),
				assets:           minimalScaffoldAssets(),
				frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
			}, "--module", modulePath, "collision-app")
			if err == nil || !strings.Contains(err.Error(), "conflicts with") {
				t.Fatalf("module-collision error = %v", err)
			}
			assertTestDirectoryEmpty(t, workingDirectory)
			if strings.Contains(output, "Created Aginex project") {
				t.Fatalf("failure output reported success: %q", output)
			}
		})
	}
}

func TestNewCommandRejectsMoreThanOneArgumentWithoutMutation(t *testing.T) {
	workingDirectory := t.TempDir()
	before := snapshotTestTree(t, workingDirectory)
	workingDirectoryCalls := 0

	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: func() (string, error) {
			workingDirectoryCalls++
			return workingDirectory, nil
		},
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	}, "first", "second")
	if err == nil || !strings.Contains(err.Error(), "at most 1 arg") {
		t.Fatalf("argument error = %v", err)
	}
	if workingDirectoryCalls != 0 {
		t.Fatalf("working directory resolved %d times before argument rejection", workingDirectoryCalls)
	}
	if after := snapshotTestTree(t, workingDirectory); !reflect.DeepEqual(after, before) {
		t.Fatalf("working directory changed: before=%#v after=%#v", before, after)
	}
	if strings.Contains(output, "Created Aginex project") {
		t.Fatalf("failure output reported success: %q", output)
	}
}

func TestValidateProjectName(t *testing.T) {
	maximumLength := "a" + strings.Repeat("b", 62)
	for _, name := range []string{
		"a",
		"testproject",
		"test-project",
		"a1-b2",
		"com0",
		"com10",
		maximumLength,
	} {
		t.Run("valid_"+name, func(t *testing.T) {
			if err := validateProjectName(name); err != nil {
				t.Fatalf("validateProjectName(%q) = %v", name, err)
			}
		})
	}

	invalid := []string{
		"",
		".",
		"..",
		"/absolute",
		`C:\absolute`,
		"nested/project",
		`nested\project`,
		"TestProject",
		"two projects",
		"-project",
		"project-",
		"project--name",
		"project_name",
		"éclair",
		"con",
		"prn",
		"aux",
		"nul",
		"com1",
		"com9",
		"lpt1",
		"lpt9",
		"a" + strings.Repeat("b", 63),
	}
	for _, name := range invalid {
		t.Run("invalid_"+name, func(t *testing.T) {
			if err := validateProjectName(name); err == nil {
				t.Fatalf("validateProjectName(%q) succeeded", name)
			}
		})
	}
}

func TestNewCommandRejectsInvalidNameWithoutEscapingWorkingDirectory(t *testing.T) {
	parent := t.TempDir()
	workingDirectory := filepath.Join(parent, "workspace")
	if err := os.Mkdir(workingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	before := snapshotTestTree(t, parent)

	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	}, "../escaped")
	if err == nil || !strings.Contains(err.Error(), "direct child name") {
		t.Fatalf("invalid-name error = %v", err)
	}
	if after := snapshotTestTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid name changed parent: before=%#v after=%#v", before, after)
	}
	if strings.Contains(output, "Created Aginex project") {
		t.Fatalf("failure output reported success: %q", output)
	}
}

func TestNewCommandRejectsNonEmptyCurrentDirectoryWithoutMutation(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "occupied-project")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	hiddenPath := filepath.Join(target, ".existing")
	if err := os.WriteFile(hiddenPath, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTestTree(t, target)

	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(target),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	})
	if err == nil || !strings.Contains(err.Error(), "is not empty") {
		t.Fatalf("non-empty error = %v", err)
	}
	if after := snapshotTestTree(t, target); !reflect.DeepEqual(after, before) {
		t.Fatalf("non-empty directory changed: before=%#v after=%#v", before, after)
	}
	if strings.Contains(output, "Created Aginex project") {
		t.Fatalf("failure output reported success: %q", output)
	}
	assertNoProjectStagingDirectories(t, parent)
}

func TestNewCommandRejectsExistingNamedTargetsWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*testing.T, string)
	}{
		{name: "regular file", arrange: func(t *testing.T, target string) {
			if err := os.WriteFile(target, []byte("existing file\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "empty directory", arrange: func(t *testing.T, target string) {
			if err := os.Mkdir(target, 0o750); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "non-empty directory", arrange: func(t *testing.T, target string) {
			if err := os.Mkdir(target, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("keep me\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symbolic link", arrange: func(t *testing.T, target string) {
			realTarget := filepath.Join(filepath.Dir(target), "real-target")
			if err := os.Mkdir(realTarget, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(realTarget, target); err != nil {
				t.Skipf("create symlink: %v", err)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workingDirectory := t.TempDir()
			target := filepath.Join(workingDirectory, "existing-project")
			test.arrange(t, target)
			before := snapshotTestTree(t, workingDirectory)

			output, err := executeNewProjectCommand(t, newProjectDependencies{
				workingDirectory: fixedWorkingDirectory(workingDirectory),
				assets:           minimalScaffoldAssets(),
				frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
			}, "existing-project")
			if err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("existing-target error = %v", err)
			}
			if after := snapshotTestTree(t, workingDirectory); !reflect.DeepEqual(after, before) {
				t.Fatalf("existing target changed: before=%#v after=%#v", before, after)
			}
			if strings.Contains(output, "Created Aginex project") {
				t.Fatalf("failure output reported success: %q", output)
			}
			assertNoProjectStagingDirectories(t, workingDirectory)
		})
	}
}

func TestNewCommandRollsBackInjectedPublicationFailures(t *testing.T) {
	publicationFailure := errors.New("injected publication failure")

	t.Run("named directory", func(t *testing.T) {
		workingDirectory := t.TempDir()
		target := filepath.Join(workingDirectory, "publish-failure")
		publishCalls := 0
		output, err := executeNewProjectCommand(t, newProjectDependencies{
			workingDirectory: fixedWorkingDirectory(workingDirectory),
			assets:           minimalScaffoldAssets(),
			frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
			publishPath: func(source, destination string) error {
				publishCalls++
				if destination != target {
					t.Fatalf("publish destination = %q, want %q", destination, target)
				}
				if _, statErr := os.Stat(filepath.Join(source, ".aginex", "project.json")); statErr != nil {
					t.Fatalf("publisher received incomplete stage: %v", statErr)
				}
				return publicationFailure
			},
		}, "publish-failure")
		if !errors.Is(err, publicationFailure) {
			t.Fatalf("publication error = %v", err)
		}
		if publishCalls != 1 {
			t.Fatalf("publish calls = %d", publishCalls)
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("failed publication left target: %v", statErr)
		}
		if strings.Contains(output, "Created Aginex project") {
			t.Fatalf("failure output reported success: %q", output)
		}
		assertNoProjectStagingDirectories(t, workingDirectory)
	})

	t.Run("current directory", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "rollback-project")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		publishCalls := 0
		output, err := executeNewProjectCommand(t, newProjectDependencies{
			workingDirectory: fixedWorkingDirectory(target),
			assets:           minimalScaffoldAssets(),
			frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
			publishPath: func(source, destination string) error {
				publishCalls++
				if publishCalls == 2 {
					return publicationFailure
				}
				return publishNewPath(source, destination)
			},
		})
		if !errors.Is(err, publicationFailure) {
			t.Fatalf("publication error = %v", err)
		}
		if publishCalls != 2 {
			t.Fatalf("publish calls = %d", publishCalls)
		}
		assertTestDirectoryEmpty(t, target)
		if strings.Contains(output, "Created Aginex project") {
			t.Fatalf("failure output reported success: %q", output)
		}
		assertNoProjectStagingDirectories(t, parent)
	})
}

func TestNewCommandPreservesPublishedPathChangedBeforeRollback(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "changed-during-publish")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	publishCalls := 0
	var changedPath string
	_, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(target),
		assets:           minimalScaffoldAssets(),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
		publishPath: func(source, destination string) error {
			publishCalls++
			if publishCalls == 1 {
				if err := publishNewPath(source, destination); err != nil {
					return err
				}
				changedPath = destination
				return os.WriteFile(destination, []byte("external change\n"), 0o644)
			}
			return errors.New("stop publication")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "preserve changed published path") {
		t.Fatalf("publication error = %v", err)
	}
	if got := string(readTestFile(t, changedPath)); got != "external change\n" {
		t.Fatalf("changed path content = %q", got)
	}
	assertNoProjectStagingDirectories(t, parent)
}

func TestNewCommandCleansStagingAfterRenderFailure(t *testing.T) {
	workingDirectory := t.TempDir()
	publishCalled := false
	output, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		assets: fstest.MapFS{
			"unsupported-link": &fstest.MapFile{
				Data: []byte("not a regular asset"),
				Mode: fs.ModeSymlink,
			},
		},
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
		publishPath: func(string, string) error {
			publishCalled = true
			return nil
		},
	}, "render-failure")
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("render error = %v", err)
	}
	if publishCalled {
		t.Fatal("publisher was called after render failure")
	}
	if strings.Contains(output, "Created Aginex project") {
		t.Fatalf("failure output reported success: %q", output)
	}
	assertTestDirectoryEmpty(t, workingDirectory)
	assertNoProjectStagingDirectories(t, workingDirectory)
}

func TestNewCommandGeneratesCompleteScaffoldWithoutLocalState(t *testing.T) {
	workingDirectory := t.TempDir()
	target := filepath.Join(workingDirectory, "complete-app")
	if _, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
		frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
	}, "complete-app"); err != nil {
		t.Fatal(err)
	}

	for _, relative := range []string{
		".aginex/project.json",
		".agents/skills/create-aginex-project/SKILL.md",
		".env.example",
		".gitignore",
		"AGENTS.md",
		"LICENSE",
		"NOTICE",
		"README.md",
		"admin/app/layout.tsx",
		"admin/lib/api.generated.ts",
		"admin/package.json",
		"backend/cmd/openapi/main.go",
		"backend/cmd/server/main.go",
		"backend/cmd/worker/main.go",
		"compose.yaml",
		"docs/openapi.json",
		"backend/go.mod",
		"backend/go.sum",
		"package.json",
		"pnpm-lock.yaml",
		"pnpm-workspace.yaml",
	} {
		info, err := os.Lstat(filepath.Join(target, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("required scaffold path %q: %v", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("required scaffold path %q is a symlink", relative)
		}
	}

	for _, relative := range []string{
		".cache",
		".env",
		".git",
		".next",
		".pnpm-store",
		"data",
		"node_modules",
		"playwright-report",
		"test-results",
	} {
		if _, err := os.Lstat(filepath.Join(target, relative)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("local-only path %q was generated: %v", relative, err)
		}
	}
	forbiddenComponents := map[string]struct{}{
		".cache":            {},
		".git":              {},
		".next":             {},
		".pnpm-store":       {},
		"data":              {},
		"node_modules":      {},
		"playwright-report": {},
		"test-results":      {},
	}
	if err := filepath.WalkDir(target, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if _, forbidden := forbiddenComponents[entry.Name()]; forbidden {
			t.Fatalf("local-only path was generated: %s", filePath)
		}
		if entry.Type().IsRegular() {
			name := entry.Name()
			if strings.HasSuffix(name, ".db") || strings.HasSuffix(name, ".db-wal") ||
				strings.HasSuffix(name, ".db-shm") || strings.HasSuffix(name, ".sqlite") ||
				strings.HasSuffix(name, ".sqlite3") {
				t.Fatalf("database state was generated: %s", filePath)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	environmentExample := string(readTestFile(t, filepath.Join(target, ".env.example")))
	for _, emptySetting := range []string{
		"AGINEX_DATABASE_DSN=",
		"AGINEX_SESSION_SECRET=",
		"AGINEX_BOOTSTRAP_ADMIN_EMAIL=",
		"AGINEX_BOOTSTRAP_ADMIN_PASSWORD=",
		"AGINEX_STORAGE_ACCESS_KEY_ID=",
		"AGINEX_STORAGE_ACCESS_KEY_SECRET=",
	} {
		if !containsExactLine(environmentExample, emptySetting) {
			t.Fatalf(".env.example does not leave %q empty", emptySetting)
		}
	}
	packageDocument := map[string]any{}
	if err := json.Unmarshal(readTestFile(t, filepath.Join(target, "package.json")), &packageDocument); err != nil {
		t.Fatal(err)
	}
	if packageDocument["name"] != "complete-app" {
		t.Fatalf("package name = %#v", packageDocument["name"])
	}
	compose := readTestFile(t, filepath.Join(target, "compose.yaml"))
	if !bytes.HasPrefix(compose, []byte("name: complete-app\n")) {
		t.Fatalf("compose header = %q", bytes.SplitN(compose, []byte("\n"), 2)[0])
	}
	assertNoProjectStagingDirectories(t, workingDirectory)
}

func TestGeneratedProjectBuildsAsExternalConsumer(t *testing.T) {
	if testing.Short() {
		t.Skip("external Go consumer verification is disabled in short mode")
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repositoryRoot, err := filepath.Abs(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, "backend", "go.mod")); os.IsNotExist(err) {
		t.Skip("external consumer integration requires the backend source checkout")
	}
	workingDirectory := t.TempDir()
	target := filepath.Join(workingDirectory, "consumer-app")
	if _, err := executeNewProjectCommand(t, newProjectDependencies{
		workingDirectory: fixedWorkingDirectory(workingDirectory),
	},
		"--aginex-path", repositoryRoot,
		"--module", "example.com/acme/consumer-app",
		"consumer-app",
	); err != nil {
		t.Fatal(err)
	}

	goModPath := filepath.Join(target, "backend/go.mod")
	goMod := readTestFile(t, goModPath)
	parsed, err := modfile.Parse(goModPath, goMod, nil)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepositoryRoot, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Replace) != 1 ||
		parsed.Replace[0].Old.Path != frameworkModulePath ||
		parsed.Replace[0].New.Path != filepath.ToSlash(filepath.Join(canonicalRepositoryRoot, "backend")) {
		t.Fatalf("generated local framework replacement = %#v", parsed.Replace)
	}

	for _, arguments := range [][]string{
		{"test", "-mod=readonly", "./..."},
		{"mod", "tidy", "-diff"},
	} {
		command := exec.Command("go", arguments...)
		command.Dir = filepath.Join(target, "backend")
		command.Env = append(
			os.Environ(),
			"GOPROXY=off",
			"GOSUMDB=off",
			"GOWORK=off",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(arguments, " "), err, output)
		}
		if len(output) != 0 && arguments[0] == "mod" {
			t.Fatalf("go mod tidy reported scaffold drift:\n%s", output)
		}
	}
}

func TestNewCommandManifestIsDeterministicAndMatchesFileHashes(t *testing.T) {
	targets := make([]string, 0, 2)
	manifests := make([][]byte, 0, 2)
	for iteration := 0; iteration < 2; iteration++ {
		workingDirectory := t.TempDir()
		target := filepath.Join(workingDirectory, "deterministic-app")
		if _, err := executeNewProjectCommand(t, newProjectDependencies{
			workingDirectory: fixedWorkingDirectory(workingDirectory),
			frameworkVersion: fixedFrameworkVersion(testFrameworkVersion),
		}, "deterministic-app"); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
		manifests = append(manifests, readTestFile(t, filepath.Join(target, ".aginex", "project.json")))
		assertNoProjectStagingDirectories(t, workingDirectory)
	}
	if !bytes.Equal(manifests[0], manifests[1]) {
		t.Fatal("identical inputs produced different project manifests")
	}
	if first, second := snapshotTestTree(t, targets[0]), snapshotTestTree(t, targets[1]); !reflect.DeepEqual(first, second) {
		t.Fatal("identical inputs produced different scaffold trees")
	}

	manifest := readProjectManifest(t, targets[0])
	if manifest.SchemaVersion != scaffoldSchema || manifest.TemplateVersion != scaffoldTemplate {
		t.Fatalf("manifest versions = schema %d template %d", manifest.SchemaVersion, manifest.TemplateVersion)
	}
	if manifest.ProjectName != "deterministic-app" || manifest.ModulePath != "deterministic-app" {
		t.Fatalf("manifest project identity = %#v", manifest)
	}
	if manifest.FrameworkModule != frameworkModulePath || manifest.FrameworkVersion != testFrameworkVersion {
		t.Fatalf("manifest framework identity = %#v", manifest)
	}

	seen := make(map[string]struct{}, len(manifest.Files))
	previous := ""
	for _, file := range manifest.Files {
		if !fs.ValidPath(file.Path) || path.Clean(file.Path) != file.Path || file.Path == ".aginex/project.json" {
			t.Fatalf("unsafe or self-referential manifest path %q", file.Path)
		}
		if previous != "" && previous >= file.Path {
			t.Fatalf("manifest paths are not strictly sorted: %q before %q", previous, file.Path)
		}
		previous = file.Path
		if _, exists := seen[file.Path]; exists {
			t.Fatalf("duplicate manifest path %q", file.Path)
		}
		seen[file.Path] = struct{}{}

		content := readTestFile(t, filepath.Join(targets[0], filepath.FromSlash(file.Path)))
		sum := sha256.Sum256(content)
		if file.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("manifest hash for %q = %q, want %x", file.Path, file.SHA256, sum)
		}
		ownership := "application"
		if file.Path == "docs/openapi.json" || file.Path == "admin/lib/api.generated.ts" {
			ownership = "generated"
		}
		if file.Ownership != ownership {
			t.Fatalf("manifest ownership for %q = %q, want %q", file.Path, file.Ownership, ownership)
		}
	}

	actualFiles := 0
	if err := filepath.WalkDir(targets[0], func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			t.Fatalf("generated scaffold contains symlink %q", filePath)
		}
		if entry.Type().IsRegular() && filePath != filepath.Join(targets[0], ".aginex", "project.json") {
			actualFiles++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != actualFiles {
		t.Fatalf("manifest files = %d, rendered regular files = %d", len(manifest.Files), actualFiles)
	}
}

func executeNewProjectCommand(
	t *testing.T,
	dependencies newProjectDependencies,
	args ...string,
) (string, error) {
	t.Helper()
	command := newProjectCommand(dependencies)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(append([]string{}, args...))
	command.SetContext(t.Context())
	err := command.Execute()
	return output.String(), err
}

func fixedWorkingDirectory(directory string) func() (string, error) {
	return func() (string, error) { return directory, nil }
}

func fixedFrameworkVersion(version string) func() (string, error) {
	return func() (string, error) { return version, nil }
}

func minimalScaffoldAssets() fs.FS {
	return fstest.MapFS{
		"asset.txt": &fstest.MapFile{Data: []byte("fixture asset\n"), Mode: 0o444},
		"backend/go.mod": &fstest.MapFile{Data: []byte(`module github.com/xgtian-root/aginex/backend

go 1.25.0

require (
	github.com/google/uuid v1.6.0
)
`), Mode: 0o444},
	}
}

func readTestFile(t *testing.T, filePath string) []byte {
	t.Helper()
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read %s: %v", filePath, err)
	}
	return content
}

func readProjectManifest(t *testing.T, root string) projectManifest {
	t.Helper()
	payload := readTestFile(t, filepath.Join(root, ".aginex", "project.json"))
	var manifest projectManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatalf("decode project manifest: %v", err)
	}
	return manifest
}

func assertTestDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("directory %s is not empty: %v", directory, names)
	}
}

func assertNoProjectStagingDirectories(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".aginex-new-") {
			t.Fatalf("staging directory was not cleaned: %s", filepath.Join(directory, entry.Name()))
		}
	}
}

func containsExactLine(document string, expected string) bool {
	for _, line := range strings.Split(document, "\n") {
		if line == expected {
			return true
		}
	}
	return false
}

func assertGeneratedGoMod(t *testing.T, content []byte, modulePath, version string) {
	t.Helper()
	if !bytes.HasPrefix(content, []byte("module "+modulePath+"\n")) ||
		!bytes.Contains(content, []byte("\nrequire "+frameworkModulePath+" "+version+"\n")) ||
		!bytes.Contains(content, []byte("\nrequire (\n")) {
		t.Fatalf("go.mod = %q", content)
	}
	if bytes.HasPrefix(content, []byte("module "+frameworkModulePath+"\n")) {
		t.Fatalf("go.mod retained framework module declaration: %q", content)
	}
}

type testTreeEntry struct {
	Mode   fs.FileMode
	SHA256 string
	Link   string
}

func snapshotTestTree(t *testing.T, root string) map[string]testTreeEntry {
	t.Helper()
	result := make(map[string]testTreeEntry)
	if err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			return err
		}
		snapshot := testTreeEntry{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(filePath)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			snapshot.SHA256 = hex.EncodeToString(sum[:])
		} else if info.Mode()&os.ModeSymlink != 0 {
			snapshot.Link, err = os.Readlink(filePath)
			if err != nil {
				return err
			}
		}
		result[filepath.ToSlash(relative)] = snapshot
		return nil
	}); err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return result
}
