package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

func projectRoot(start string) (string, error) {
	directory, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		found := true
		for _, file := range []string{"server/go.mod", "admin/package.json", "go.work"} {
			info, err := os.Stat(filepath.Join(directory, file))
			if err != nil || !info.Mode().IsRegular() {
				found = false
				break
			}
		}
		if found {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("no Aginex project root above %s (expected server/go.mod, admin/package.json and go.work)", start)
		}
		directory = parent
	}
}

func projectProcess(ctx context.Context, cmd *cobra.Command, root, name string, args ...string) *exec.Cmd {
	process := exec.CommandContext(ctx, name, args...)
	process.Dir = root
	process.Stdin = cmd.InOrStdin()
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	process.Cancel = func() error {
		err := process.Process.Signal(os.Interrupt)
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			return process.Process.Kill()
		}
		return err
	}
	process.WaitDelay = 15 * time.Second
	return process
}

// Build in the backend module, then run from the project root. This preserves
// runtime paths and delivers cancellation and exit codes to the actual binary.
func backendProcess(ctx context.Context, cmd *cobra.Command, root, entry string, args ...string) (*exec.Cmd, func(), error) {
	directory, err := os.MkdirTemp("", "aginex-tool-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	binary := filepath.Join(directory, entry)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := projectProcess(ctx, cmd, root, "go", "-C", "server", "build", "-o", binary, "./cmd/"+entry)
	build.Stdin = nil
	if err := build.Run(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("build backend %s: %w", entry, err)
	}
	return projectProcess(ctx, cmd, root, binary, args...), cleanup, nil
}
