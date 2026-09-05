// Package devtools implements local development maintenance tools.
package devtools

import (
	"context"
	"github.com/spf13/cobra"
	"io"
)

// Execute runs backend maintenance commands with the caller's streams and context.
func Execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	root := &cobra.Command{Use: "aginex-tool", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newReinitializeCommand(reinitializeDependencies{}))
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root.ExecuteContext(ctx)
}
