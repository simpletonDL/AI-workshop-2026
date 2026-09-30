// Package cli implements the atlas command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// NewRootCommand builds the root atlas command with all subcommands attached.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "atlas",
		Short:         "Inspect Claude Code skills in a git repository",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newListSkillsCommand(), newServeCommand())
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd := NewRootCommand()
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(stderr, "Error:", err)
		return 1
	}
	return 0
}

// Main is the entry point used by cmd/atlas.
func Main() {
	os.Exit(Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
