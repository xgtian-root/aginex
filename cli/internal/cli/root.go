package cli

import (
	"bufio"
	"context"
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
	"github.com/xgtian-root/aginex/cli/internal/buildinfo"
)

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newRootCommand().ExecuteContext(ctx)
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{Use: "aginex", Short: "Build and verify Aginex admin applications", SilenceUsage: true, SilenceErrors: true}
	root.Version = buildinfo.String()
	root.AddCommand(newProjectCommand(newProjectDependencies{}), doctorCommand(), checkCommand(), devCommand(), generateCommand(), skillsCommand())
	return root
}

func devCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "dev", Short: "Run the API and admin development servers together",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := projectRoot(".")
			if err != nil {
				return err
			}
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			server, cleanup, err := backendProcess(ctx, cmd, root, "server")
			if err != nil {
				return err
			}
			defer cleanup()
			admin := projectProcess(ctx, cmd, root, "pnpm", "dev:admin")
			processes := []*exec.Cmd{server, admin}
			if durableWorkerConfigured() {
				worker, workerCleanup, err := backendProcess(ctx, cmd, root, "worker")
				if err != nil {
					return err
				}
				defer workerCleanup()
				processes = append(processes, worker)
			}
			results := make(chan error, len(processes))
			started := 0
			for _, process := range processes {
				if err := process.Start(); err != nil {
					cancel()
					for range started {
						<-results
					}
					return err
				}
				started++
				go func(p *exec.Cmd) { results <- p.Wait() }(process)
			}
			if len(processes) == 3 {
				fmt.Fprintln(cmd.OutOrStdout(), "Aginex API, admin, and durable worker are starting. Press Ctrl+C to stop.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Aginex API and admin development servers are starting. Durable jobs are disabled. Press Ctrl+C to stop.")
			}
			first := <-results
			interrupted := cmd.Context().Err() != nil
			cancel()
			for i := 1; i < started; i++ {
				<-results
			}
			if interrupted {
				return nil
			}
			return first
		},
	}
	command.AddCommand(&cobra.Command{
		Use: "reinitialize", Short: "Archive stale pre-release local state and return to browser Setup", DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot(".")
			if err != nil {
				return err
			}
			process, cleanup, err := backendProcess(cmd.Context(), cmd, root, "aginex-tool", append([]string{"reinitialize"}, args...)...)
			if err != nil {
				return err
			}
			defer cleanup()
			return process.Run()
		},
	})
	command.AddCommand(&cobra.Command{
		Use: "reconcile-storage-presentation", Short: "Reconcile verified OSS object presentation metadata", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot(".")
			if err != nil {
				return err
			}
			process, cleanup, err := backendProcess(cmd.Context(), cmd, root, "aginex-tool", append([]string{"reconcile-storage-presentation"}, args...)...)
			if err != nil {
				return err
			}
			defer cleanup()
			return process.Run()
		},
	})
	return command
}

func durableWorkerConfigured() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGINEX_JOBS_DRIVER")), "postgres")
}

func generateCommand() *cobra.Command {
	command := &cobra.Command{Use: "generate", Short: "Generate deterministic Aginex artifacts"}
	command.AddCommand(&cobra.Command{
		Use: "client", Short: "Regenerate OpenAPI and the TypeScript client contract",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := projectRoot(".")
			if err != nil {
				return err
			}
			process, cleanup, err := backendProcess(cmd.Context(), cmd, root, "openapi", "-output", "docs/openapi.json")
			if err != nil {
				return err
			}
			defer cleanup()
			if err := process.Run(); err != nil {
				return err
			}
			return projectProcess(cmd.Context(), cmd, root, "pnpm", "--filter", "@aginex/admin", "generate:client").Run()
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
			root, err := projectRoot(directory)
			if err != nil {
				return err
			}
			checks := diagnose(root)
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
	for _, file := range []string{"server/go.mod", "go.work", "admin/package.json", "package.json", "pnpm-workspace.yaml", "AGENTS.md"} {
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
			root, err := projectRoot(".")
			if err != nil {
				return err
			}
			steps := [][]string{{"go", "-C", "server", "test", "./..."}}
			if _, err := os.Stat(filepath.Join(root, "cli", "go.mod")); err == nil {
				steps = append(steps, []string{"go", "-C", "cli", "test", "./..."}, []string{"go", "run", "./cli/cmd/sync-templates", "-check"})
			}
			for _, step := range steps {
				fmt.Fprintf(cmd.OutOrStdout(), "\n→ %s\n", strings.Join(step, " "))
				process := projectProcess(cmd.Context(), cmd, root, step[0], step[1:]...)
				process.Stdout = cmd.OutOrStdout()
				process.Stderr = cmd.ErrOrStderr()
				process.Stdin = cmd.InOrStdin()
				if err := process.Run(); err != nil {
					return fmt.Errorf("%s: %w", strings.Join(step, " "), err)
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\n→ aginex skills validate")
			count, err := validateSkills(filepath.Join(root, ".agents", "skills"))
			if err != nil {
				return fmt.Errorf("aginex skills validate: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Validated %d canonical Agent Skills.\n", count)

			webSteps := [][]string{
				{"pnpm", "check:admin"},
				{"pnpm", "test:admin"},
			}
			if !skipBuild {
				webSteps = append(webSteps, []string{"pnpm", "build:admin"})
			}
			for _, step := range webSteps {
				fmt.Fprintf(cmd.OutOrStdout(), "\n→ %s\n", strings.Join(step, " "))
				process := projectProcess(cmd.Context(), cmd, root, step[0], step[1:]...)
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
			root, err := projectRoot(".")
			if len(args) == 0 && err != nil {
				return err
			}
			directory := filepath.Join(root, ".agents", "skills")
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
