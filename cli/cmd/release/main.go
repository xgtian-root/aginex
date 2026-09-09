// release implements the local build and CI distribution pipeline. It has no
// backend package imports and never changes source files or creates Git tags.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const repository = "xgtian-root/aginex"
const tapRepository = "xgtian-root/homebrew-tap"
const cliModule = "github.com/xgtian-root/aginex/cli"
const backendModule = "github.com/xgtian-root/aginex/server"

type options struct {
	root, dist, release, version string
	all                          bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release {build|verify|smoke|publish|verify-install|tap} [options]")
	}
	var o options
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.StringVar(&o.root, "root", "..", "Aginex source root")
	f.StringVar(&o.dist, "dist", "", "artifact directory (default: <root>/cli/dist)")
	switch args[0] {
	case "build":
		f.BoolVar(&o.all, "all", false, "build and archive all six platforms")
		f.StringVar(&o.release, "release", "", "build a release from its matching cli/vX.Y.Z tag")
	case "tap", "verify-install":
		f.StringVar(&o.version, "version", "", "published CLI version, including v prefix")
	case "verify", "smoke", "publish":
	default:
		return fmt.Errorf("unknown operation %q", args[0])
	}
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	emptyRelease := false
	f.Visit(func(option *flag.Flag) {
		if option.Name == "release" && o.release == "" {
			emptyRelease = true
		}
	})
	if emptyRelease {
		return errors.New("--release requires a non-empty version")
	}
	var err error
	o.root, err = filepath.Abs(o.root)
	if err != nil {
		return err
	}
	if o.dist == "" {
		o.dist = filepath.Join(o.root, "cli", "dist")
	}
	o.dist, err = filepath.Abs(o.dist)
	if err != nil {
		return err
	}
	switch args[0] {
	case "build":
		return build(o)
	case "verify":
		_, err = verifyBundle(o.dist)
		return err
	case "smoke":
		return smoke(o)
	case "publish":
		return publish(o)
	case "verify-install":
		return verifyInstall(o.version)
	case "tap":
		return updateTap(o.version)
	}
	return nil
}

// Every subprocess is bounded, and environment overrides replace inherited
// values so GOOS/GOARCH/GOWORK from a developer shell cannot affect host tools.
func command(dir string, env map[string]string, input []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := env[key]; !replaced {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w\n%s%s", name, strings.Join(args, " "), err, output, stderr.String())
	}
	return output, nil
}

func git(root string, args ...string) (string, error) {
	output, err := command(root, nil, nil, "git", args...)
	return strings.TrimSpace(string(output)), err
}
