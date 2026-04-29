package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/miropshq/mirops-cli/internal/providers"
	"github.com/spf13/cobra"
)

var (
	source        string
	apiURL        string
	apiToken      string
	cluster       string
	threshold     int
	enforce       bool
	targetVersion string
	output        string
	timeout       time.Duration
	retry         int
)

type Report struct {
	GeneratedAt    string `json:"generatedAt"`
	TTLSeconds     int    `json:"ttlSeconds"`
	Cluster        string `json:"cluster"`
	ClusterName    string `json:"clusterName"`
	ClusterVersion string `json:"clusterVersion"`
	TargetVersion  string `json:"targetVersion"`
	RiskScore      int    `json:"riskScore"`
	Threshold      int    `json:"threshold"`
	Allow          bool   `json:"allow"`
	Reason         string `json:"reason"`
}

// isSaaSMode returns true when any SaaS flag or its env var is set.
func isSaaSMode(cmd *cobra.Command) bool {
	return apiURL != "" || apiToken != "" || cluster != ""
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Evaluate upgrade risk and gate pipeline",
	Long: `Evaluate Kubernetes upgrade risk.

OSS mode  — provide --source pointing to a local file, S3, Azure Blob, or HTTP URL.
SaaS mode — provide --api-url, --api-token, and --cluster to fetch the report from
            the Mirops backend (requires a paid subscription).`,
	Run: func(cmd *cobra.Command, args []string) {
		// Resolve env var fallbacks
		if source == "" {
			source = os.Getenv("MIROPS_SOURCE")
		}
		if apiURL == "" {
			apiURL = os.Getenv("MIROPS_API_URL")
		}
		if apiToken == "" {
			apiToken = os.Getenv("MIROPS_API_TOKEN")
		}
		if cluster == "" {
			cluster = os.Getenv("MIROPS_CLUSTER")
		}
		if !cmd.Flags().Changed("threshold") {
			if v := os.Getenv("MIROPS_THRESHOLD"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					threshold = n
				}
			}
		}
		if !cmd.Flags().Changed("enforce") {
			enforce = os.Getenv("MIROPS_ENFORCE") == "true"
		}
		if targetVersion == "" {
			targetVersion = os.Getenv("MIROPS_TARGET_VERSION")
		}
		if !cmd.Flags().Changed("output") {
			if v := os.Getenv("MIROPS_OUTPUT"); v != "" {
				output = v
			}
		}
		if !cmd.Flags().Changed("timeout") {
			if v := os.Getenv("MIROPS_TIMEOUT"); v != "" {
				if d, err := time.ParseDuration(v); err == nil {
					timeout = d
				}
			}
		}
		if !cmd.Flags().Changed("retry") {
			if v := os.Getenv("MIROPS_RETRY"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					retry = n
				}
			}
		}

		if isSaaSMode(cmd) {
			// SaaS mode: all three SaaS flags are required
			missing := []string{}
			if apiURL == "" {
				missing = append(missing, "--api-url (env: MIROPS_API_URL)")
			}
			if apiToken == "" {
				missing = append(missing, "--api-token (env: MIROPS_API_TOKEN)")
			}
			if cluster == "" {
				missing = append(missing, "--cluster (env: MIROPS_CLUSTER)")
			}
			if len(missing) > 0 {
				for _, m := range missing {
					fmt.Fprintf(os.Stderr, "Error: SaaS mode requires %s\n", m)
				}
				os.Exit(2)
			}
			// TODO: fetch report from Mirops backend using apiURL, apiToken, cluster
			fmt.Fprintln(os.Stderr, "Error: SaaS mode not yet implemented")
			os.Exit(2)
		}

		// OSS mode: --source is required
		if source == "" {
			fmt.Fprintln(os.Stderr, "Error: --source is required in OSS mode (or set MIROPS_SOURCE)")
			os.Exit(2)
		}

		provider, err := providers.Validate(source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(2)
		}

		data, err := provider.Fetch(source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching source: %v\n", err)
			os.Exit(2)
		}

		var report Report
		if err := json.Unmarshal(data, &report); err != nil {
			fmt.Fprintf(os.Stderr, "Invalid JSON: %v\n", err)
			os.Exit(2)
		}

		// Use the report's threshold unless the flag was explicitly passed
		effectiveThreshold := threshold
		if !cmd.Flags().Changed("threshold") && report.Threshold > 0 {
			effectiveThreshold = report.Threshold
		}

		blocked := report.RiskScore > effectiveThreshold

		switch output {
		case "json":
			result := map[string]interface{}{
				"cluster":        report.Cluster,
				"clusterName":    report.ClusterName,
				"clusterVersion": report.ClusterVersion,
				"targetVersion":  report.TargetVersion,
				"riskScore":      report.RiskScore,
				"threshold":      effectiveThreshold,
				"allow":          !blocked,
				"reason":         report.Reason,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(result)
		default:
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "Mirops Upgrade Analysis")
			fmt.Fprintln(w, "─────────────────────────────────────")
			fmt.Fprintf(w, "Cluster:\t%s (%s)\n", report.ClusterName, report.Cluster)
			fmt.Fprintf(w, "Cluster Version:\t%s\n", report.ClusterVersion)
			fmt.Fprintf(w, "Target Version:\t%s\n", report.TargetVersion)
			fmt.Fprintf(w, "Risk Score:\t%d\n", report.RiskScore)
			fmt.Fprintf(w, "Threshold:\t%d\n", effectiveThreshold)
			fmt.Fprintf(w, "Reason:\t%s\n", report.Reason)
			w.Flush()
			if blocked {
				fmt.Println("❌ BLOCKED")
			} else {
				fmt.Println("✔ SAFE")
			}
		}

		if enforce && blocked {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)

	// OSS flags
	scanCmd.Flags().StringVar(&source, "source", "", "[OSS] Report source: path, file://, s3://, azure://, http:// (env: MIROPS_SOURCE)")

	// SaaS flags (require a paid subscription)
	scanCmd.Flags().StringVar(&apiURL, "api-url", "", "[SaaS] Mirops backend URL (env: MIROPS_API_URL)")
	scanCmd.Flags().StringVar(&apiToken, "api-token", "", "[SaaS] Authentication token (env: MIROPS_API_TOKEN)")
	scanCmd.Flags().StringVar(&cluster, "cluster", "", "[SaaS] Cluster identifier (env: MIROPS_CLUSTER)")

	// Common flags
	scanCmd.Flags().IntVar(&threshold, "threshold", 70, "Risk threshold (env: MIROPS_THRESHOLD)")
	scanCmd.Flags().BoolVar(&enforce, "enforce", false, "Block pipeline if risk exceeds threshold (env: MIROPS_ENFORCE)")
	scanCmd.Flags().StringVar(&targetVersion, "target-version", "", "Expected target version to validate (env: MIROPS_TARGET_VERSION)")
	scanCmd.Flags().StringVar(&output, "output", "table", "Output format: table, json (env: MIROPS_OUTPUT)")
	scanCmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Request timeout (env: MIROPS_TIMEOUT)")
	scanCmd.Flags().IntVar(&retry, "retry", 3, "Number of retries on failure (env: MIROPS_RETRY)")
}
