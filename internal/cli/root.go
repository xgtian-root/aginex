package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/xgtian-root/aginex/framework/application"
	"github.com/xgtian-root/aginex/internal/buildinfo"
	"github.com/xgtian-root/aginex/internal/composition"
)

func Execute() error {
	return ExecuteWithDefinition(composition.Definition())
}

// ExecuteWithDefinition runs the CLI with the same immutable module set used
// by a derived application's API, worker, and OpenAPI generator.
func ExecuteWithDefinition(definition application.Definition) error {
	return newRootCommandWithDefinition(definition).Execute()
}

func newRootCommand() *cobra.Command {
	return newRootCommandWithDefinition(composition.Definition())
}

func newRootCommandWithDefinition(
	definition application.Definition,
) *cobra.Command {
	root := &cobra.Command{
		Use:           "aginex",
		Short:         "Build and verify Aginex admin applications",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.Version = buildinfo.String()
	root.AddCommand(
		doctorCommand(),
		checkCommand(),
		devCommand(),
		generateCommand(definition),
		skillsCommand(),
	)
	return root
}

func devCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dev",
		Short: "Run the API and web development servers together",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			commands := []*exec.Cmd{
				exec.CommandContext(ctx, "go", "run", "./cmd/server"),
				exec.CommandContext(ctx, "pnpm", "dev:web"),
			}
			results := make(chan error, len(commands))
			for _, process := range commands {
				process.Stdout = cmd.OutOrStdout()
				process.Stderr = cmd.ErrOrStderr()
				process.Stdin = cmd.InOrStdin()
				if err := process.Start(); err != nil {
					cancel()
					return err
				}
				go func(running *exec.Cmd) {
					results <- running.Wait()
				}(process)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Aginex API and web development servers are starting. Press Ctrl+C to stop.")
			select {
			case <-ctx.Done():
				return nil
			case err := <-results:
				cancel()
				return err
			}
		},
	}
}

func generateCommand(
	definition application.Definition,
) *cobra.Command {
	command := &cobra.Command{Use: "generate", Short: "Generate deterministic Aginex artifacts"}
	command.AddCommand(&cobra.Command{
		Use:   "client",
		Short: "Regenerate OpenAPI and the TypeScript client contract",
		RunE: func(cmd *cobra.Command, _ []string) error {
			document, err := json.MarshalIndent(
				definition.BuildOpenAPI(),
				"",
				"  ",
			)
			if err != nil {
				return fmt.Errorf("marshal OpenAPI: %w", err)
			}
			document = append(document, '\n')
			output := filepath.Join("docs", "openapi.json")
			if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
				return fmt.Errorf("create OpenAPI directory: %w", err)
			}
			if err := os.WriteFile(output, document, 0o640); err != nil {
				return fmt.Errorf("write OpenAPI: %w", err)
			}
			process := exec.Command(
				"pnpm",
				"--filter",
				"@aginex/web",
				"generate:client",
			)
			process.Stdout = cmd.OutOrStdout()
			process.Stderr = cmd.ErrOrStderr()
			process.Stdin = cmd.InOrStdin()
			return process.Run()
		},
	})
	return command
}

type diagnostic struct {
	Name     string
	Required bool
	Detail   string
	Err      error
}

func doctorCommand() *cobra.Command {
	var directory string
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Inspect the local Aginex development environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := diagnose(directory)
			failed := false
			for _, check := range checks {
				symbol := "✓"
				if check.Err != nil {
					if check.Required {
						symbol = "✗"
						failed = true
					} else {
						symbol = "!"
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %-14s %s\n", symbol, check.Name, check.Detail)
			}
			if failed {
				return errors.New("required Aginex checks failed")
			}
			return nil
		},
	}
	command.Flags().StringVarP(&directory, "directory", "C", ".", "project directory")
	return command
}

func diagnose(directory string) []diagnostic {
	checks := []diagnostic{
		toolDiagnostic("Go", true, "go", "version"),
		toolDiagnostic("Node.js", true, "node", "--version"),
		toolDiagnostic("pnpm", true, "pnpm", "--version"),
		toolDiagnostic("Docker", false, "docker", "--version"),
	}
	for _, file := range []string{"go.mod", "package.json", "pnpm-workspace.yaml", "AGENTS.md"} {
		path := filepath.Join(directory, file)
		_, err := os.Stat(path)
		detail := path
		if err != nil {
			detail = "missing " + path
		}
		checks = append(checks, diagnostic{Name: file, Required: true, Detail: detail, Err: err})
	}
	skills, err := validateSkills(filepath.Join(directory, ".agents", "skills"))
	detail := fmt.Sprintf("%d canonical Skills", skills)
	if err != nil {
		detail = err.Error()
	}
	checks = append(checks, diagnostic{Name: "Agent Skills", Required: true, Detail: detail, Err: err})
	return checks
}

func toolDiagnostic(name string, required bool, executable string, args ...string) diagnostic {
	path, err := exec.LookPath(executable)
	if err != nil {
		return diagnostic{Name: name, Required: required, Detail: executable + " is not on PATH", Err: err}
	}
	output, commandErr := exec.Command(path, args...).CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = path
	}
	return diagnostic{Name: name, Required: required, Detail: detail, Err: commandErr}
}

func checkCommand() *cobra.Command {
	var skipBuild bool
	command := &cobra.Command{
		Use:   "check",
		Short: "Run the deterministic Aginex verification gate",
		RunE: func(cmd *cobra.Command, _ []string) error {
			steps := [][]string{
				{"go", "test", "./..."},
				{"go", "run", "./cmd/aginex", "skills", "validate"},
				{"pnpm", "check:web"},
				{"pnpm", "test:web"},
			}
			if !skipBuild {
				steps = append(steps, []string{"pnpm", "build:web"})
			}
			for _, step := range steps {
				fmt.Fprintf(cmd.OutOrStdout(), "\n→ %s\n", strings.Join(step, " "))
				process := exec.Command(step[0], step[1:]...)
				process.Stdout = cmd.OutOrStdout()
				process.Stderr = cmd.ErrOrStderr()
				process.Stdin = cmd.InOrStdin()
				if err := process.Run(); err != nil {
					return fmt.Errorf("%s: %w", strings.Join(step, " "), err)
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\nAll Aginex checks passed.")
			return nil
		},
	}
	command.Flags().BoolVar(&skipBuild, "skip-build", false, "skip the production web build")
	return command
}

func skillsCommand() *cobra.Command {
	command := &cobra.Command{Use: "skills", Short: "Manage canonical Agent Skills"}
	command.AddCommand(&cobra.Command{
		Use:   "validate [directory]",
		Short: "Validate Skill names, frontmatter, and metadata",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			directory := filepath.Join(".agents", "skills")
			if len(args) == 1 {
				directory = args[0]
			}
			count, err := validateSkills(directory)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Validated %d Agent Skills in %s.\n", count, directory)
			return nil
		},
	})
	return command
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validateSkills(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("read Skills directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return 0, errors.New("no Skills found")
	}
	for _, directoryName := range names {
		if !skillNamePattern.MatchString(directoryName) {
			return 0, fmt.Errorf("invalid Skill directory name %q", directoryName)
		}
		skillPath := filepath.Join(root, directoryName, "SKILL.md")
		name, description, err := readFrontmatter(skillPath)
		if err != nil {
			return 0, err
		}
		if name != directoryName {
			return 0, fmt.Errorf("%s: frontmatter name %q does not match directory", skillPath, name)
		}
		if len(description) < 40 {
			return 0, fmt.Errorf("%s: description must explain purpose and triggers", skillPath)
		}
		metadata := filepath.Join(root, directoryName, "agents", "openai.yaml")
		if _, err := os.Stat(metadata); err != nil {
			return 0, fmt.Errorf("%s: missing agents/openai.yaml", directoryName)
		}
	}
	return len(names), nil
}

func readFrontmatter(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || scanner.Text() != "---" {
		return "", "", fmt.Errorf("%s: missing YAML frontmatter", path)
	}
	values := map[string]string{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	for key := range values {
		if key != "name" && key != "description" {
			return "", "", fmt.Errorf("%s: unsupported frontmatter key %q", path, key)
		}
	}
	if values["name"] == "" || values["description"] == "" {
		return "", "", fmt.Errorf("%s: name and description are required", path)
	}
	return values["name"], values["description"], nil
}
