package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/miropshq/mirops-cli/internal/providers"
	"github.com/miropshq/mirops-cli/internal/report"
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

		var r report.Report
		if err := json.Unmarshal(data, &r); err != nil {
			fmt.Fprintf(os.Stderr, "Invalid JSON: %v\n", err)
			os.Exit(2)
		}

		// Use the report's threshold unless the flag was explicitly passed
		var thresholdOverride int
		if cmd.Flags().Changed("threshold") {
			thresholdOverride = threshold
		}
		blocked, level, effectiveThreshold := r.Analyze(thresholdOverride)

		switch output {
		case "json":
			result := map[string]interface{}{
				"cluster":        r.Cluster,
				"clusterVersion": r.ClusterVersion,
				"targetVersion":  r.TargetVersion,
				"riskScore":      r.Scores.Total,
				"threshold":      effectiveThreshold,
				"level":          level,
				"allow":          !blocked,
				"reason":         r.Reason,
				"issues":         r.Issues,
			}
			// Emit AI scoring metadata only when AI actually ran.
			if r.Scores.AI.Ran() {
				result["ai"] = r.Scores.AI
			}
			if r.AIReasoning != "" {
				result["aiReasoning"] = r.AIReasoning
			}
			// Emit the logical-mirror sections for other tools (omit when absent).
			if len(r.Addons) > 0 {
				result["addons"] = r.Addons
			}
			if r.Risk != nil {
				result["risk"] = r.Risk
			}
			if r.Graph != nil {
				result["graph"] = r.Graph
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(result)
		default:
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "Mirops Upgrade Analysis")
			fmt.Fprintln(w, "─────────────────────────────────────")
			fmt.Fprintf(w, "Cluster:\t%s\n", r.Cluster)
			fmt.Fprintf(w, "Cluster Version:\t%s\n", r.ClusterVersion)
			fmt.Fprintf(w, "Target Version:\t%s\n", r.TargetVersion)
			fmt.Fprintln(w, "─────────────────────────────────────")
			fmt.Fprintf(w, "Score Total:\t%d / 100\n", r.Scores.Total)
			if b := r.Scores.Base; b != nil {
				fmt.Fprintf(w, "  Health:\t%d\n", b.Health)
				fmt.Fprintf(w, "  Capacity:\t%d\n", b.Capacity)
				fmt.Fprintf(w, "  Stability:\t%d\n", b.Stability)
				fmt.Fprintf(w, "  Risk:\t%d\n", b.Risk)
			}
			if ai := r.Scores.AI; ai.Ran() {
				line := fmt.Sprintf("  AI (%s):\t%d", ai.Weight, ai.Score)
				if ai.Model != "" {
					line += fmt.Sprintf("  [%s]", ai.Model)
				}
				fmt.Fprintln(w, line)
			}
			fmt.Fprintln(w, "─────────────────────────────────────")
			fmt.Fprintf(w, "Threshold:\t%d\n", effectiveThreshold)
			fmt.Fprintf(w, "Reason:\t%s\n", r.Reason)
			if r.AIReasoning != "" {
				fmt.Fprintf(w, "AI Reasoning:\t%s\n", r.AIReasoning)
			}
			if len(r.Issues) > 0 {
				fmt.Fprintln(w, "Issues:")
				for _, issue := range r.Issues {
					fmt.Fprintf(w, "  · %s\n", issue)
				}
			}
			w.Flush()

			renderAddons(r.Addons)
			renderNamespaceRisk(r.Risk)
			renderMirrorSummary(r)

			switch level {
			case "BLOCK":
				fmt.Println("❌ BLOCK — Do not upgrade, cluster is unhealthy")
			case "WARNING":
				fmt.Println("⚠️  WARNING — Upgrade possible but issues detected")
			default:
				fmt.Println("✔ SAFE — Cluster ready, upgrade recommended")
			}
		}

		if enforce && blocked {
			os.Exit(1)
		}
	},
}

// renderAddons prints the add-on compatibility table. Skips when there are none.
func renderAddons(addons []report.AddonCompatibility) {
	if len(addons) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("ADD-ONS")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, a := range addons {
		version := a.Version
		if version == "" {
			version = "-"
		}
		var status string
		switch a.Status {
		case "compatible":
			status = "✓ compatible"
		case "incompatible":
			status = "✗ incompatible"
			if a.RequiredVersion != "" {
				status += fmt.Sprintf("\t→ upgrade to %s", a.RequiredVersion)
			}
		default:
			status = "? unknown"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", a.Name, version, status)
	}
	w.Flush()
}

// renderNamespaceRisk prints per-namespace risk, sorted desc, skipping risk 0.
func renderNamespaceRisk(risk *report.RiskBreakdown) {
	if risk == nil || len(risk.ByNamespace) == 0 {
		return
	}
	rows := make([]report.NamespaceRisk, 0, len(risk.ByNamespace))
	for _, ns := range risk.ByNamespace {
		if ns.Risk > 0 {
			rows = append(rows, ns)
		}
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Risk > rows[j].Risk })

	fmt.Println()
	fmt.Println("NAMESPACE RISK")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, ns := range rows {
		severity := fmt.Sprintf("%s (%d)", report.RiskSeverity(ns.Risk), ns.Risk)
		fmt.Fprintf(w, "%s\t%s\t%d/%d at risk\n", ns.Namespace, severity, ns.AtRisk, ns.Components)
	}
	w.Flush()
}

// renderMirrorSummary prints a one-line summary of the logical mirror.
func renderMirrorSummary(r report.Report) {
	if r.Graph == nil {
		return
	}
	atRisk := 0
	for _, n := range r.Graph.Nodes {
		if n.Risk >= 50 {
			atRisk++
		}
	}
	incompatible := 0
	for _, a := range r.Addons {
		if a.Status == "incompatible" {
			incompatible++
		}
	}
	fmt.Println()
	fmt.Printf("MIRROR: %d components, %d at risk, %d incompatible add-ons\n",
		len(r.Graph.Nodes), atRisk, incompatible)
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
