package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// envPrefix makes every flag of every command settable from the environment, so a pipeline is
// configured once through variables: --target-version ↔ MIROPS_TARGET_VERSION, -n/--namespace ↔
// MIROPS_NAMESPACE. Precedence is flag > environment > default.
const envPrefix = "MIROPS_"

// envName is the environment variable for a flag: dashes become underscores, upper-cased.
func envName(flag string) string {
	return envPrefix + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// bindEnv applies the MIROPS_* variables to every flag not set on the command line. A variable that
// doesn't parse (MIROPS_ENFORCE=maybe) is an error, not silently ignored: a misconfigured pipeline must
// stop, never run with a default it didn't ask for.
func bindEnv(cmd *cobra.Command) error {
	var errs []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			return
		}
		v, ok := os.LookupEnv(envName(f.Name))
		if !ok || v == "" {
			return
		}
		if err := f.Value.Set(v); err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q: %v", envName(f.Name), v, err))
		}
	})
	if len(errs) > 0 {
		return fmt.Errorf("invalid environment: %s", strings.Join(errs, "; "))
	}
	return nil
}
