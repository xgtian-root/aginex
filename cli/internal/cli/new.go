package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/spf13/cobra"
	scaffoldassets "github.com/xgtian-root/aginex/cli/templates"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	frameworkModulePath = "github.com/xgtian-root/aginex/server"
	scaffoldSchema      = 1
	scaffoldTemplate    = 1
)

var portableProjectName = regexp.MustCompile(
	`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`,
)

var errUnpublishedFrameworkBuild = errors.New(
	"source-built Aginex CLI has no clean, pinned framework version; use --aginex-path with a local Aginex checkout",
)

type newProjectDependencies struct {
	workingDirectory func() (string, error)
	assets           fs.FS
	frameworkVersion func() (string, error)
	publishPath      func(string, string) error
}

func (dependencies newProjectDependencies) withDefaults() newProjectDependencies {
	if dependencies.workingDirectory == nil {
		dependencies.workingDirectory = os.Getwd
	}
	if dependencies.assets == nil {
		dependencies.assets = scaffoldassets.ProjectTemplateFS()
	}
	if dependencies.frameworkVersion == nil {
		dependencies.frameworkVersion = bundledFrameworkVersion
	}
	if dependencies.publishPath == nil {
		dependencies.publishPath = publishNewPath
	}
	return dependencies
}

func newProjectCommand(dependencies newProjectDependencies) *cobra.Command {
	dependencies = dependencies.withDefaults()
	var requestedModulePath string
	var requestedAginexPath string
	command := &cobra.Command{
		Use:   "new [name]",
		Short: "Initialize a new Aginex application",
		Long: "Initialize the current directory when no name is supplied, or " +
			"create and initialize a new child directory when a name is supplied.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(command *cobra.Command, args []string) error {
			if requestedModulePath != "" {
				if err := validateApplicationModulePath(requestedModulePath); err != nil {
					return err
				}
			}
			workingDirectory, err := dependencies.workingDirectory()
			if err != nil {
				return fmt.Errorf("resolve current directory: %w", err)
			}
			workingDirectory, err = filepath.Abs(workingDirectory)
			if err != nil {
				return fmt.Errorf("resolve absolute current directory: %w", err)
			}
			localAginexPath, err := resolveLocalAginexPath(
				workingDirectory,
				requestedAginexPath,
			)
			if err != nil {
				return err
			}

			projectName := inferProjectName(filepath.Base(workingDirectory))
			target := workingDirectory
			currentDirectoryMode := len(args) == 0
			if !currentDirectoryMode {
				projectName = args[0]
				if err := validateProjectName(projectName); err != nil {
					return err
				}
				target = filepath.Join(workingDirectory, projectName)
				if err := requireMissingTarget(target); err != nil {
					return err
				}
			} else if err := requireEmptyDirectory(target); err != nil {
				return err
			}

			modulePath := requestedModulePath
			if modulePath == "" {
				modulePath = projectName
				if err := validateReservedModulePathSegments(modulePath); err != nil {
					return err
				}
			}

			version, err := dependencies.frameworkVersion()
			if err != nil && localAginexPath != "" && errors.Is(err, errUnpublishedFrameworkBuild) {
				version, err = normalizeFrameworkVersion(scaffoldassets.BackendVersion)
			}
			if err != nil {
				return fmt.Errorf("resolve Aginex framework version: %w", err)
			}
			model := scaffoldModel{
				ProjectName:      projectName,
				ModulePath:       modulePath,
				FrameworkVersion: version,
				FrameworkPath:    localAginexPath,
			}
			stage, err := os.MkdirTemp(
				filepath.Dir(target),
				".aginex-new-"+projectName+"-",
			)
			if err != nil {
				return fmt.Errorf("create project staging directory: %w", err)
			}
			defer os.RemoveAll(stage)

			if err := renderScaffold(stage, dependencies.assets, model); err != nil {
				return fmt.Errorf("render project: %w", err)
			}
			if err := os.Chmod(stage, 0o755); err != nil {
				return fmt.Errorf("set project directory permissions: %w", err)
			}

			if currentDirectoryMode {
				if err := publishIntoCurrentDirectory(
					stage,
					target,
					dependencies.publishPath,
				); err != nil {
					return err
				}
			} else if err := dependencies.publishPath(stage, target); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return fmt.Errorf("target directory %q already exists", target)
				}
				return fmt.Errorf("publish project to %q: %w", target, err)
			}

			fmt.Fprintf(command.OutOrStdout(), "Created Aginex project %s in %s.\n", projectName, target)
			fmt.Fprintln(command.OutOrStdout(), "Next: run `pnpm install`, then `aginex dev`.")
			return nil
		},
	}
	command.Flags().StringVar(
		&requestedModulePath,
		"module",
		"",
		"Go module path (defaults to the project name)",
	)
	command.Flags().StringVar(
		&requestedAginexPath,
		"aginex-path",
		"",
		"local Aginex checkout for an unreleased development build",
	)
	return command
}

func validateApplicationModulePath(modulePath string) error {
	if err := module.CheckPath(modulePath); err != nil {
		return fmt.Errorf("invalid Go module path %q: %w", modulePath, err)
	}
	return validateReservedModulePathSegments(modulePath)
}

func validateReservedModulePathSegments(modulePath string) error {
	for _, segment := range strings.Split(modulePath, "/") {
		if segment == "vendor" {
			return fmt.Errorf(
				"invalid Go module path %q: the reserved path segment %q would make generated imports unusable",
				modulePath,
				segment,
			)
		}
	}
	return nil
}

func resolveLocalAginexPath(workingDirectory, requestedPath string) (string, error) {
	if requestedPath == "" {
		return "", nil
	}
	resolved := requestedPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workingDirectory, resolved)
	}
	resolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve local Aginex checkout %q: %w", requestedPath, err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve local Aginex checkout %q: %w", requestedPath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect local Aginex checkout %q: %w", requestedPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("local Aginex checkout %q is not a directory", requestedPath)
	}
	resolved = filepath.Join(resolved, "server")
	goModPath := filepath.Join(resolved, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("read local Aginex checkout module: %w", err)
	}
	parsed, err := modfile.Parse(goModPath, goMod, nil)
	if err != nil {
		return "", fmt.Errorf("parse local Aginex checkout module: %w", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != frameworkModulePath {
		return "", fmt.Errorf(
			"local Aginex checkout %q must declare module %s",
			requestedPath,
			frameworkModulePath,
		)
	}
	return filepath.ToSlash(resolved), nil
}

func validateProjectName(name string) error {
	if name == "" {
		return errors.New("project name is required")
	}
	if len(name) > 63 {
		return errors.New("project name must be at most 63 bytes")
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\\`) || name == "." || name == ".." {
		return fmt.Errorf("project name %q must be a direct child name, not a path", name)
	}
	if !portableProjectName.MatchString(name) {
		return fmt.Errorf(
			"project name %q must start with a lowercase letter and contain only lowercase letters, numbers, and single hyphens",
			name,
		)
	}
	if isReservedWindowsName(name) {
		return fmt.Errorf("project name %q is reserved on Windows", name)
	}
	return nil
}

func isReservedWindowsName(name string) bool {
	switch name {
	case "con", "prn", "aux", "nul":
		return true
	}
	if len(name) == 4 && (strings.HasPrefix(name, "com") || strings.HasPrefix(name, "lpt")) {
		return name[3] >= '1' && name[3] <= '9'
	}
	return false
}

func inferProjectName(directoryName string) string {
	var builder strings.Builder
	lastWasHyphen := false
	for _, character := range strings.ToLower(directoryName) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
			lastWasHyphen = false
			continue
		}
		if !lastWasHyphen && builder.Len() > 0 && (unicode.IsSpace(character) || unicode.IsPunct(character) || unicode.IsSymbol(character)) {
			builder.WriteByte('-')
			lastWasHyphen = true
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		name = "aginex-app"
	}
	if name[0] < 'a' || name[0] > 'z' {
		name = "app-" + name
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if isReservedWindowsName(name) {
		name = "app-" + name
	}
	return name
}

func requireMissingTarget(target string) error {
	_, err := os.Lstat(target)
	if err == nil {
		return fmt.Errorf(
			"target directory %q already exists; enter an empty directory and run `aginex new` instead",
			target,
		)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect target directory %q: %w", target, err)
	}
	return nil
}

func requireEmptyDirectory(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect current directory %q: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("current target %q must be a real directory", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return fmt.Errorf("read current directory %q: %w", target, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("current directory %q is not empty", target)
	}
	return nil
}

type scaffoldModel struct {
	ProjectName      string
	ModulePath       string
	FrameworkVersion string
	FrameworkPath    string
}

type projectManifest struct {
	SchemaVersion    int            `json:"schemaVersion"`
	TemplateVersion  int            `json:"templateVersion"`
	ProjectName      string         `json:"projectName"`
	ModulePath       string         `json:"modulePath"`
	FrameworkModule  string         `json:"frameworkModule"`
	FrameworkVersion string         `json:"frameworkVersion"`
	Files            []manifestFile `json:"files"`
}

type manifestFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Ownership string `json:"ownership"`
}

func renderScaffold(root string, assets fs.FS, model scaffoldModel) error {
	if err := copyScaffoldAssets(root, assets, model); err != nil {
		return err
	}
	generated := map[string]string{
		"README.md":                                 projectReadme,
		"server/cmd/aginex-tool/main.go":            projectToolMain,
		"go.work":                                   projectWorkspace,
		"server/cmd/openapi/main.go":                projectOpenAPIMain,
		"server/cmd/server/main.go":                 projectServerMain,
		"server/cmd/worker/main.go":                 projectWorkerMain,
		"server/internal/composition/definition.go": projectComposition,
	}
	paths := make([]string, 0, len(generated))
	for file := range generated {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	for _, file := range paths {
		if err := renderTemplateFile(root, file, generated[file], model); err != nil {
			return err
		}
	}

	files, err := scaffoldFileManifest(root)
	if err != nil {
		return err
	}
	manifest := projectManifest{
		SchemaVersion:    scaffoldSchema,
		TemplateVersion:  scaffoldTemplate,
		ProjectName:      model.ProjectName,
		ModulePath:       model.ModulePath,
		FrameworkModule:  frameworkModulePath,
		FrameworkVersion: model.FrameworkVersion,
		Files:            files,
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project manifest: %w", err)
	}
	payload = append(payload, '\n')
	if err := writeExclusiveFile(
		filepath.Join(root, ".aginex", "project.json"),
		payload,
	); err != nil {
		return fmt.Errorf("write project manifest: %w", err)
	}
	return nil
}

func copyScaffoldAssets(root string, assets fs.FS, model scaffoldModel) error {
	return fs.WalkDir(assets, ".", func(assetPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if assetPath == "." {
			return nil
		}
		if !fs.ValidPath(assetPath) || path.Clean(assetPath) != assetPath {
			return fmt.Errorf("invalid embedded asset path %q", assetPath)
		}
		relativePath := strings.TrimSuffix(assetPath, ".template")
		destination := filepath.Join(root, filepath.FromSlash(relativePath))
		if entry.IsDir() {
			if err := os.Mkdir(destination, 0o755); err != nil {
				return fmt.Errorf("create asset directory %q: %w", assetPath, err)
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("embedded asset %q is not a regular file", assetPath)
		}
		content, err := fs.ReadFile(assets, assetPath)
		if err != nil {
			return fmt.Errorf("read embedded asset %q: %w", assetPath, err)
		}
		content, err = customizeScaffoldAsset(relativePath, content, model)
		if err != nil {
			return err
		}
		if err := writeExclusiveFile(destination, content); err != nil {
			return fmt.Errorf("write embedded asset %q: %w", assetPath, err)
		}
		return nil
	})
}

func customizeScaffoldAsset(assetPath string, content []byte, model scaffoldModel) ([]byte, error) {
	switch assetPath {
	case "server/go.mod":
		return projectGoMod(content, model)
	case "server/go.sum":
		return projectGoSum(content), nil
	case "package.json":
		var document map[string]any
		if err := json.Unmarshal(content, &document); err != nil {
			return nil, fmt.Errorf("decode embedded package.json: %w", err)
		}
		document["name"] = model.ProjectName
		result, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode project package.json: %w", err)
		}
		return append(result, '\n'), nil
	case "compose.yaml":
		prefix := []byte("name: aginex\n")
		if !strings.HasPrefix(string(content), string(prefix)) {
			return nil, errors.New("embedded compose.yaml is missing its project name header")
		}
		return append(
			[]byte("name: "+model.ProjectName+"\n"),
			content[len(prefix):]...,
		), nil
	default:
		return content, nil
	}
}

func projectGoSum(frameworkGoSum []byte) []byte { return frameworkGoSum }

func projectGoMod(frameworkGoMod []byte, model scaffoldModel) ([]byte, error) {
	parsed, err := modfile.Parse("embedded go.mod", frameworkGoMod, nil)
	if err != nil {
		return nil, fmt.Errorf("parse embedded go.mod: %w", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != frameworkModulePath {
		return nil, errors.New("embedded go.mod has an unexpected module path")
	}
	if parsed.Go == nil || strings.TrimSpace(parsed.Go.Version) == "" {
		return nil, errors.New("embedded go.mod is missing its Go version")
	}
	if model.ModulePath == parsed.Module.Mod.Path {
		return nil, fmt.Errorf(
			"Go module path %q conflicts with the Aginex framework module",
			model.ModulePath,
		)
	}
	for _, requirement := range parsed.Require {
		if model.ModulePath == requirement.Mod.Path {
			return nil, fmt.Errorf(
				"Go module path %q conflicts with an Aginex dependency module",
				model.ModulePath,
			)
		}
	}

	type dependency struct {
		path    string
		version string
	}
	dependencies := make([]dependency, 0, len(parsed.Require))
	for _, requirement := range parsed.Require {
		dependencies = append(dependencies, dependency{
			path:    requirement.Mod.Path,
			version: requirement.Mod.Version,
		})
	}
	sort.Slice(dependencies, func(left, right int) bool {
		return dependencies[left].path < dependencies[right].path
	})

	var result strings.Builder
	fmt.Fprintf(&result, "module %s\n\ngo %s\n\n", model.ModulePath, parsed.Go.Version)
	fmt.Fprintf(&result, "require %s %s\n", frameworkModulePath, model.FrameworkVersion)
	if len(dependencies) > 0 {
		result.WriteString("\nrequire (\n")
		for _, dependency := range dependencies {
			fmt.Fprintf(
				&result,
				"\t%s %s // indirect\n",
				dependency.path,
				dependency.version,
			)
		}
		result.WriteString(")\n")
	}
	if model.FrameworkPath != "" {
		fmt.Fprintf(
			&result,
			"\nreplace %s => %s\n",
			frameworkModulePath,
			modfile.AutoQuote(model.FrameworkPath),
		)
	}
	return []byte(result.String()), nil
}

func renderTemplateFile(root, relativePath, source string, model scaffoldModel) error {
	tmpl, err := template.New(relativePath).Option("missingkey=error").Parse(source)
	if err != nil {
		return fmt.Errorf("parse template %q: %w", relativePath, err)
	}
	var output strings.Builder
	if err := tmpl.Execute(&output, model); err != nil {
		return fmt.Errorf("render template %q: %w", relativePath, err)
	}
	if err := writeExclusiveFile(
		filepath.Join(root, filepath.FromSlash(relativePath)),
		[]byte(output.String()),
	); err != nil {
		return fmt.Errorf("write template %q: %w", relativePath, err)
	}
	return nil
}

func writeExclusiveFile(destination string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func scaffoldFileManifest(root string) ([]manifestFile, error) {
	var result []manifestFile
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("rendered path %q is not a regular file", filePath)
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		sum := sha256.Sum256(content)
		ownership := "application"
		if relative == "docs/openapi.json" || relative == "admin/lib/api.generated.ts" {
			ownership = "generated"
		}
		result = append(result, manifestFile{
			Path:      relative,
			SHA256:    hex.EncodeToString(sum[:]),
			Ownership: ownership,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inventory rendered project: %w", err)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Path < result[right].Path
	})
	return result, nil
}

func publishIntoCurrentDirectory(
	stage string,
	target string,
	publish func(string, string) error,
) error {
	if err := requireEmptyDirectory(target); err != nil {
		return fmt.Errorf("publish into current directory: %w", err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		return fmt.Errorf("read rendered project: %w", err)
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].Name() == ".aginex" {
			return false
		}
		if entries[right].Name() == ".aginex" {
			return true
		}
		return entries[left].Name() < entries[right].Name()
	})
	type publishedPath struct {
		path        string
		fingerprint [sha256.Size]byte
	}
	published := make([]publishedPath, 0, len(entries))
	rollback := func() error {
		var rollbackErrors []error
		for index := len(published) - 1; index >= 0; index-- {
			current, err := fingerprintPath(published[index].path)
			if err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf(
					"preserve changed published path %q: %w",
					published[index].path,
					err,
				))
				continue
			}
			if current != published[index].fingerprint {
				rollbackErrors = append(rollbackErrors, fmt.Errorf(
					"preserve changed published path %q",
					published[index].path,
				))
				continue
			}
			if err := os.RemoveAll(published[index].path); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
		return errors.Join(rollbackErrors...)
	}
	for _, entry := range entries {
		source := filepath.Join(stage, entry.Name())
		destination := filepath.Join(target, entry.Name())
		fingerprint, err := fingerprintPath(source)
		if err != nil {
			return errors.Join(
				fmt.Errorf("fingerprint rendered path %q: %w", entry.Name(), err),
				rollback(),
			)
		}
		if err := publish(source, destination); err != nil {
			publishErr := fmt.Errorf("publish %q: %w", entry.Name(), err)
			if errors.Is(err, fs.ErrExist) {
				publishErr = fmt.Errorf("target path %q appeared while creating the project", destination)
			}
			return errors.Join(publishErr, rollback())
		}
		published = append(published, publishedPath{
			path:        destination,
			fingerprint: fingerprint,
		})
	}
	return nil
}

func fingerprintPath(root string) ([sha256.Size]byte, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(
			hash,
			"%s\x00%s\x00",
			filepath.ToSlash(relative),
			info.Mode().String(),
		); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(filePath)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(filePath)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(hash, target); err != nil {
				return err
			}
		}
		return nil
	})
	var result [sha256.Size]byte
	if err != nil {
		return result, err
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func bundledFrameworkVersion() (string, error) {
	mainVersion := ""
	modified := false
	if info, ok := debug.ReadBuildInfo(); ok {
		mainVersion = strings.TrimSpace(info.Main.Version)
		for _, setting := range info.Settings {
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				modified = true
				break
			}
		}
	}
	return resolveFrameworkBuildVersion(mainVersion, scaffoldassets.BackendVersion, modified)
}

func resolveFrameworkBuildVersion(mainVersion, declaredVersion string, modified bool) (string, error) {
	mainVersion = strings.TrimSpace(mainVersion)
	dirty := modified || strings.HasSuffix(mainVersion, "+dirty")
	// The CLI module version is independent of the bundled backend version.
	version, err := normalizeFrameworkVersion(declaredVersion)
	if err != nil {
		return "", err
	}
	if dirty || strings.Contains(semver.Prerelease(version), "dev") {
		return "", fmt.Errorf("%w (declared version %s)", errUnpublishedFrameworkBuild, version)
	}
	return version, nil
}

func normalizeFrameworkVersion(version string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "" || version == "unknown" {
		return "", errors.New("the CLI does not contain a usable framework version")
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return "", fmt.Errorf("%q is not a semantic version", version)
	}
	return semver.Canonical(version), nil
}

const projectComposition = `// Package composition owns the module set used by every application process.
package composition

import "github.com/xgtian-root/aginex/server/framework/application"

var definition = mustDefine()

// Definition returns this application's immutable composition root.
func Definition() application.Definition {
	return definition
}

func mustDefine() application.Definition {
	result, err := application.Define(
		application.FilesModule(),
		application.StarterExampleModule(),
	)
	if err != nil {
		panic(err)
	}
	return result
}
`

const projectServerMain = `package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"{{.ModulePath}}/internal/composition"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := composition.Definition().RunAPI(ctx); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}
`

const projectOpenAPIMain = `package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"{{.ModulePath}}/internal/composition"
)

func main() {
	output := flag.String("output", "docs/openapi.json", "OpenAPI output file")
	flag.Parse()
	document, err := json.MarshalIndent(composition.Definition().BuildOpenAPI(), "", "  ")
	if err != nil {
		exit(err)
	}
	document = append(document, '\n')
	if err := os.MkdirAll(filepath.Dir(*output), 0o750); err != nil {
		exit(err)
	}
	if err := os.WriteFile(*output, document, 0o640); err != nil {
		exit(err)
	}
	fmt.Printf("Wrote %s\n", *output)
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
`

const projectWorkerMain = `package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"{{.ModulePath}}/internal/composition"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := composition.Definition().RunWorker(ctx); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}
`

const projectReadme = `# {{.ProjectName}}

This application was created with Aginex {{.FrameworkVersion}}.

## Start development

1. Install Web dependencies with ` + "`pnpm install`" + `.
2. Run ` + "`aginex dev`" + ` from this directory.
3. Open http://localhost:3000 and complete browser Setup.

The API listens on http://localhost:8080 by default. Configuration examples
live in ` + "`server/.env.example` and `admin/.env.example`" + `; copy them to each service's
` + "`.env`" + ` to customize startup. Process environment overrides those files.
Use ` + "`aginex config list`" + ` to inspect settings and ` + "`aginex config set admin PORT=3001`" + `
to save and apply changes to a running development session. Database connection
changes are tested first and require interactive confirmation before saving.
Real environment files and credentials are ignored by Git.

## Verify and generate contracts

- ` + "`aginex doctor`" + ` checks the local toolchain and project shape.
- ` + "`aginex check`" + ` runs backend and Web verification.
- ` + "`aginex generate client`" + ` regenerates OpenAPI from this project's
  composition and updates the typed Web client.

Development defaults to disabled durable jobs. Set
` + "`AGINEX_JOBS_DRIVER=postgres`" + ` when using Files with PostgreSQL;
` + "`aginex dev`" + ` then starts and supervises the independent worker with the API and admin.
A production Files deployment must run the API and worker as separate services.

Application modules are selected in ` + "`server/internal/composition/definition.go`" + `.
The Aginex framework version is pinned in ` + "`server/go.mod`" + `, while
` + "`.aginex/project.json`" + ` records the initial file hashes and ownership boundary for
safe future upgrades.
{{if .FrameworkPath}}
This project currently uses a local Aginex checkout through a development-only
` + "`replace`" + ` directive in ` + "`server/go.mod`" + `. Remove that directive and pin a downloadable
Aginex source-commit version before sharing or releasing the application.
{{end}}
`

const projectWorkspace = `go 1.25.0

use ./server
`

const projectToolMain = `package main

import (
 "context"
 "fmt"
 "os"
 "os/signal"
 "syscall"
 "github.com/xgtian-root/aginex/server/framework/devtools"
)

func main() {
 ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
 defer stop()
 if err := devtools.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
  fmt.Fprintln(os.Stderr, err)
  os.Exit(1)
 }
}
`
