package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestProjectRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"backend/go.mod", "admin/package.json", "go.work"} {
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "admin", "app", "login")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := projectRoot(nested)
	if err != nil || got != root {
		t.Fatalf("root = %q, error = %v", got, err)
	}
	if _, err := projectRoot(t.TempDir()); err == nil {
		t.Fatal("accepted an unrelated directory")
	}
}

func TestBackendProcessPreservesRootStreamsArgumentsAndExitCode(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "backend", "cmd", "probe")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"backend/go.mod": "module example.com/probe\n\ngo 1.25.0\n",
		"go.work":        "go 1.25.0\n\nuse ./backend\n",
		"backend/cmd/probe/main.go": `package main
import ("fmt"; "os"; "io")
func main() { wd, _ := os.Getwd(); fmt.Println(wd); fmt.Println(os.Args[1]); io.Copy(os.Stdout, os.Stdin); fmt.Fprintln(os.Stderr, "probe error"); os.Exit(7) }
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader("probe input"))
	process, cleanup, err := backendProcess(t.Context(), cmd, root, "probe", "argument with spaces")
	if err != nil {
		t.Fatal(err)
	}
	binary := process.Path
	err = process.Run()
	cleanup()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit = %v", err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	reported, err := filepath.EvalSymlinks(strings.SplitN(stdout.String(), "\n", 2)[0])
	if err != nil {
		t.Fatal(err)
	}
	if reported != canonical || !strings.Contains(stdout.String(), "argument with spaces\nprobe input") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "probe error") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("temporary binary retained: %v", err)
	}
}

func TestProjectProcessCancellationReapsChild(t *testing.T) {
	if os.Getenv("AGINEX_PROCESS_TEST_CHILD") == "1" {
		time.Sleep(time.Minute)
		return
	}
	ctx, cancel := context.WithCancel(t.Context())
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	process := projectProcess(ctx, cmd, t.TempDir(), os.Args[0], "-test.run=^TestProjectProcessCancellationReapsChild$")
	process.Env = append(os.Environ(), "AGINEX_PROCESS_TEST_CHILD=1")
	if err := process.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := process.Wait(); err == nil {
		t.Fatal("cancelled child unexpectedly succeeded")
	}
	if process.ProcessState == nil {
		t.Fatal("child was not reaped")
	}
}
