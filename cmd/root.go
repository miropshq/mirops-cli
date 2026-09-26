package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "mirops",
	Short: "Mirops CLI — gate Kubernetes changes on the live cluster mirror",
	// Every flag can also come from a MIROPS_* environment variable (see env.go).
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// A bad MIROPS_* value is a configuration error, not a usage error: print the message, not the
		// whole flag list.
		cmd.SilenceUsage = true
		return bindEnv(cmd)
	},
}

// SetVersion injects the build-time version into the root command.
func SetVersion(v string) {
	rootCmd.Version = v
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
//
// A bad flag or environment variable means the CLI couldn't evaluate anything, so it exits 2 like
// every other "couldn't evaluate" case; exit 1 is reserved for a change that was evaluated and blocked.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(exitCannotEvaluate)
	}
}

func init() {
}
