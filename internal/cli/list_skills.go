package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/example/atlas/internal/skills"
)

type listSkillsOptions struct {
	ref      string
	jsonMode bool
}

func newListSkillsCommand() *cobra.Command {
	var opts listSkillsOptions
	cmd := &cobra.Command{
		Use:   "list-skills <repo-url>",
		Short: "List skills (SKILL.md files) in a git repository",
		Long: `Shallow-clones the repository into a temporary directory, finds all
SKILL.md files and prints their name and description.

The repository may be an https or ssh URL, or a local path.`,
		Example: `  atlas list-skills https://github.com/anthropics/skills
  atlas list-skills git@github.com:org/repo.git --ref v1.2.0
  atlas list-skills . --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListSkills(cmd, args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.ref, "ref", "", "branch or tag to use (defaults to the default branch)")
	cmd.Flags().BoolVar(&opts.jsonMode, "json", false, "print output as JSON")
	return cmd
}

func runListSkills(cmd *cobra.Command, source string, opts listSkillsOptions) error {
	dir, cleanup, err := checkout(cmd.Context(), source, opts.ref)
	defer cleanup()
	if err != nil {
		return err
	}

	found, warnings, err := skills.Discover(dir)
	if err != nil {
		return fmt.Errorf("scan repository: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "Warning:", w)
	}

	out := cmd.OutOrStdout()
	if opts.jsonMode {
		return writeJSON(out, found)
	}
	if len(found) == 0 {
		_, err := fmt.Fprintln(out, "No skills found")
		return err
	}
	return writeTable(out, found)
}

func writeJSON(w io.Writer, list []skills.Skill) error {
	if list == nil {
		list = []skills.Skill{} // encode as [] rather than null
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func writeTable(w io.Writer, list []skills.Skill) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDESCRIPTION")
	for _, s := range list {
		fmt.Fprintf(tw, "%s\t%s\n", oneLine(s.Name), oneLine(s.Description))
	}
	return tw.Flush()
}

// oneLine collapses all whitespace (including newlines and tabs, which would
// break the table layout) into single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
