package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/xgtian-root/aginex/cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
