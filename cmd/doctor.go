package cmd

import (
	"github.com/ceilingfish/lumberjack/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that required host prerequisites (git, gh) are available",
		Long: "Verifies that git and the GitHub CLI (gh) can be found and that gh " +
			"is authenticated, reporting each tool's location and version. Exits " +
			"non-zero if any check fails, so it can be used in scripts.\n\n" +
			"doctor is CLI-local: it does not require a running daemon.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ok, err := doctor.Run(cmd.Context(), cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if !ok {
				return doctor.ErrChecksFailed
			}
			return nil
		},
	}
}
