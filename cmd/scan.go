package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
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
	enforce       bool
	enforceLevel  string
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
		if !cmd.Flags().Changed("enforce") {
			enforce = os.Getenv("MIROPS_ENFORCE") == "true"
		}
		if !cmd.Flags().Changed("enforce-level") {
			if v := os.Getenv("MIROPS_ENFORCE_LEVEL"); v != "" {
				enforceLevel = v
			}
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

		// Trust the operator's decision — gate and level come from the report.
		allow, level, blockers := r.Analyze()

		switch output {
		case "json":
			result := map[string]interface{}{
				"cluster":        r.Cluster,
				"clusterVersion": r.ClusterVersion,
				"targetVersion":  r.TargetVersion,
				"riskScore":      r.Scores.Total,
				"level":          level,
				"allow":          allow,
				"blockers":       blockers,
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
			// Full workload inventory, so a downstream tool gets the same detail as the table view.
			result["workloads"] = r.Workloads
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(result)
		default:
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, boldc("MIROPS UPGRADE ANALYSIS"))
			fmt.Fprintln(w, dim("─────────────────────────────────────"))
			fmt.Fprintf(w, "Cluster:\t%s\n", r.Cluster)
			fmt.Fprintf(w, "Upgrade:\t%s\n", boldc(r.ClusterVersion+"  →  "+r.TargetVersion))
			fmt.Fprintln(w, dim("─────────────────────────────────────"))
			fmt.Fprintf(w, "Score Total:\t%s\n", scoreColor(r.Scores.Total))
			if b := r.Scores.Base; b != nil {
				fmt.Fprintf(w, "  Health:\t%d\n", b.Health)
				fmt.Fprintf(w, "  Capacity:\t%d\n", b.Capacity)
				fmt.Fprintf(w, "  Stability:\t%d\n", b.Stability)
				fmt.Fprintf(w, "  Compatibility:\t%d\n", b.Compatibility)
			}
			if ai := r.Scores.AI; ai.Ran() {
				line := fmt.Sprintf("  AI (%s):\t%d", ai.Weight, ai.Score)
				if ai.Model != "" {
					line += fmt.Sprintf("  [%s]", ai.Model)
				}
				fmt.Fprintln(w, line)
			}
			fmt.Fprintln(w, "─────────────────────────────────────")
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
			renderWorkloads(r.Workloads)
			renderMirrorSummary(r)

			// Verdict wording matches the operator/plugin: the score is a health gauge, the verdict
			// is the go/no-go. CRITICAL = blocked, WARNING = not recommended (not blocked), SAFE = allowed.
			// It is printed last, after the full inventory, as the gate the whole report builds up to.
			fmt.Println()
			fmt.Println(dim("═══════════════════ VERDICT ═══════════════════"))
			switch level {
			case "CRITICAL":
				fmt.Println(red("❌ Upgrade blocked"))
				for _, b := range blockers {
					fmt.Printf("   %s %s\n", red("•"), b)
				}
			case "WARNING":
				fmt.Println(yellow("⚠️  Not recommended — issues detected, but not blocked"))
				if r.Reason != "" {
					fmt.Printf("   %s %s\n", yellow("•"), r.Reason)
				}
			default:
				fmt.Println(green("✔ Upgrade allowed — cluster is ready"))
			}
		}

		// Gate the pipeline. Default: fail only when the upgrade is not allowed
		// (CRITICAL). --enforce-level=warning is stricter (also fails on WARNING).
		if enforce && (!allow || report.Severity(level) >= report.Severity(enforceLevel)) {
			os.Exit(1)
		}
	},
}

// renderAddons prints the add-on compatibility table. Skips when there are none.
func renderAddons(addons []report.AddonCompatibility) {
	if len(addons) == 0 {
		return
	}
	w := section("ADD-ONS", len(addons),
		"Detected add-ons vs. the target Kubernetes version — incompatible ones must be upgraded first.")
	thead(w, "NAME", "VERSION", "STATUS")
	for _, a := range addons {
		version := a.Version
		if version == "" {
			version = "—"
		}
		var status string
		switch a.Status {
		case "compatible":
			status = green("✓ compatible")
		case "incompatible":
			status = red("✗ incompatible")
			if a.RequiredVersion != "" {
				status += red(" → upgrade to " + a.RequiredVersion)
			}
		default:
			status = yellow("? unknown")
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\n", a.Name, version, status)
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

	w := section("NAMESPACE RISK", len(rows),
		"Namespaces ranked by their riskiest component (0–100) — where upgrade impact concentrates.")
	thead(w, "NAMESPACE", "AT RISK", "SEVERITY")
	for _, ns := range rows {
		severity := fmt.Sprintf("%s (%d)", report.RiskSeverity(ns.Risk), ns.Risk)
		switch {
		case ns.Risk >= 70:
			severity = red(severity)
		case ns.Risk >= 40:
			severity = yellow(severity)
		default:
			severity = green(severity)
		}
		fmt.Fprintf(w, "  %s\t%d/%d at risk\t%s\n", ns.Namespace, ns.AtRisk, ns.Components, severity)
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

// ---- color helpers -------------------------------------------------------
// ANSI color is enabled only on a real terminal (and never when NO_COLOR is set), so piped or
// captured output stays clean. Colored tokens are always placed in a row's LAST tab-column, because
// tabwriter never pads the final cell — coloring an aligned middle column would break the columns.

var useColor = colorEnabled()

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func dim(s string) string    { return paint("2", s) }
func boldc(s string) string  { return paint("1", s) }
func green(s string) string  { return paint("32", s) }
func yellow(s string) string { return paint("33", s) }
func red(s string) string    { return paint("31", s) }

// scoreColor tints the readiness score (a gauge, not the gate): green healthy, yellow fair, red low.
func scoreColor(total int) string {
	s := fmt.Sprintf("%d / 100", total)
	switch {
	case total >= 85:
		return green(s)
	case total >= 60:
		return yellow(s)
	default:
		return red(s)
	}
}

// statusColor tints a status/phase word by health: green ok, yellow pending-ish, red bad.
func statusColor(s string) string {
	switch s {
	case "Ready", "Bound", "Running", "Complete", "Completed", "Active", "compatible":
		return green(s)
	case "Pending", "unknown", "Unknown":
		return yellow(s)
	case "":
		return s
	default: // NotReady, Down, Lost, ImagePullBackOff, CrashLoopBackOff, Failed, incompatible…
		return red(s)
	}
}

// ---- report renderer ------------------------------------------------------

// renderWorkloads prints the operator's FULL cluster inventory, section by section. Each block leads
// with its count and a one-line note on why it matters for an upgrade, then lists every row (healthy
// included) so the output is the complete report. Truly empty sections are skipped; the verdict, which
// the caller prints after this, always lands last.
func renderWorkloads(w report.Workloads) {
	renderNodes(w.Nodes)
	renderReplicaWorkloads("DEPLOYMENTS",
		"Every Deployment and its readiness — not-ready pods may not reschedule during a node drain.", w.Deployments)
	renderReplicaWorkloads("STATEFULSETS",
		"Stateful workloads — a not-ready pod here can't simply move to another node during the drain.", w.StatefulSets)
	renderDaemonSets(w.DaemonSets)
	renderJobs(w.Jobs)
	renderBarePods(w.BarePods)
	renderPVCs(w.PVCs)
	renderPDBs(w.PDBs)
	renderDeprecatedAPIs(w.DeprecatedAPIs)
}

// section prints a titled block header ("TITLE (count)" + a dimmed reason) and returns a tabwriter.
func section(title string, count int, why string) *tabwriter.Writer {
	fmt.Println()
	fmt.Printf("%s %s\n", boldc(title), dim(fmt.Sprintf("(%d)", count)))
	fmt.Printf("  %s\n", dim(why))
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// thead prints the column-title row into the section's tabwriter. Left uncolored so the ANSI-free
// widths line up with the data rows (only a row's last cell may carry color).
func thead(w *tabwriter.Writer, cols ...string) {
	fmt.Fprintf(w, "  %s\n", strings.Join(cols, "\t"))
}

// podSummary condenses a workload's problem pods (reason/restarts) to one line, capped so a 15-replica
// CrashLoopBackOff doesn't flood the output — the rest collapse to "+N more". Left uncolored because it
// sits in an aligned middle column.
func podSummary(pods []report.PodReport) string {
	const cap = 3
	probs := make([]string, 0, len(pods))
	for _, p := range pods {
		if p.Reason == "" && p.Restarts == 0 {
			continue
		}
		s := p.Name
		if p.Reason != "" {
			s += " " + p.Reason
		}
		if p.Restarts > 0 {
			s += fmt.Sprintf(" (%d restarts)", p.Restarts)
		}
		probs = append(probs, s)
	}
	if len(probs) == 0 {
		return "healthy"
	}
	if len(probs) > cap {
		return strings.Join(probs[:cap], ", ") + fmt.Sprintf(", +%d more", len(probs)-cap)
	}
	return strings.Join(probs, ", ")
}

func renderNodes(nodes []report.NodeReport) {
	if len(nodes) == 0 {
		return
	}
	w := section("NODES", len(nodes), "Cluster nodes and their readiness — the capacity to reschedule pods during a drain.")
	thead(w, "NAME", "CONDITIONS", "STATUS")
	for _, n := range nodes {
		conds := "—"
		if len(n.Conditions) > 0 {
			conds = strings.Join(n.Conditions, ", ")
		}
		// last column colored (status); name + conditions stay in aligned cells.
		fmt.Fprintf(w, "  %s\t%s\t%s\n", n.Name, conds, statusColor(n.Status))
	}
	w.Flush()
}

func renderReplicaWorkloads(title, why string, rows []report.WorkloadReport) {
	if len(rows) == 0 {
		return
	}
	w := section(title, len(rows), why)
	thead(w, "NAMESPACE/NAME", "PROBLEM PODS", "READY")
	for _, d := range rows {
		ready := fmt.Sprintf("%d/%d ready", d.ReadyReplicas, d.DesiredReplicas)
		if d.ReadyReplicas >= d.DesiredReplicas {
			ready = green(ready)
		} else {
			ready = red(ready)
		}
		// pods (aligned, uncolored) then ready (last, colored).
		fmt.Fprintf(w, "  %s/%s\t%s\t%s\n", d.Namespace, d.Name, podSummary(d.Pods), ready)
	}
	w.Flush()
}

func renderDaemonSets(rows []report.DaemonSetReport) {
	if len(rows) == 0 {
		return
	}
	w := section("DAEMONSETS", len(rows), "Per-node agents — unavailable pods mean an agent is already missing before the drain.")
	thead(w, "NAMESPACE/NAME", "PROBLEM PODS", "UNAVAILABLE")
	for _, d := range rows {
		state := fmt.Sprintf("%d unavailable", d.NumberUnavailable)
		if d.NumberUnavailable == 0 {
			state = green(state)
		} else {
			state = red(state)
		}
		fmt.Fprintf(w, "  %s/%s\t%s\t%s\n", d.Namespace, d.Name, podSummary(d.Pods), state)
	}
	w.Flush()
}

func renderJobs(rows []report.JobReport) {
	if len(rows) == 0 {
		return
	}
	w := section("JOBS", len(rows), "Stuck or failed Jobs can hold finalizers or storage that complicate a drain.")
	thead(w, "NAMESPACE/NAME", "ACTIVE", "STATUS")
	for _, j := range rows {
		status := j.Status
		if j.Reason != "" {
			status += " (" + j.Reason + ")"
		}
		fmt.Fprintf(w, "  %s/%s\t%d active\t%s\n", j.Namespace, j.Name, j.Active, statusColor(status))
	}
	w.Flush()
}

func renderBarePods(rows []report.BarePodReport) {
	if len(rows) == 0 {
		return
	}
	w := section("STANDALONE PODS", len(rows), "Pods with no controller won't be recreated if they're evicted during a drain.")
	thead(w, "NAMESPACE/NAME", "STATUS")
	for _, p := range rows {
		fmt.Fprintf(w, "  %s/%s\t%s\n", p.Namespace, p.Name, statusColor(p.Status))
	}
	w.Flush()
}

func renderPVCs(rows []report.PVCReport) {
	if len(rows) == 0 {
		return
	}
	w := section("PERSISTENT VOLUME CLAIMS", len(rows), "A PVC that isn't Bound can't reattach after a drain — Lost/Pending storage blocks the move.")
	thead(w, "NAMESPACE/NAME", "STORAGECLASS", "STATUS")
	for _, p := range rows {
		sc := p.StorageClass
		if sc == "" {
			sc = "—"
		}
		fmt.Fprintf(w, "  %s/%s\t%s\t%s\n", p.Namespace, p.Name, sc, statusColor(p.Phase))
	}
	w.Flush()
}

func renderPDBs(rows []report.PDBReport) {
	if len(rows) == 0 {
		return
	}
	w := section("POD DISRUPTION BUDGETS", len(rows), "These PDBs can deadlock a node drain if they don't allow enough disruptions.")
	thead(w, "NAMESPACE/NAME")
	for _, p := range rows {
		fmt.Fprintf(w, "  %s/%s\n", p.Namespace, p.Name)
	}
	w.Flush()
}

func renderDeprecatedAPIs(rows []report.DeprecatedAPIReport) {
	if len(rows) == 0 {
		return
	}
	w := section("DEPRECATED / REMOVED APIS", len(rows), "These API versions are removed in the target release — manifests using them fail after the upgrade.")
	thead(w, "API", "REMOVAL")
	for _, a := range rows {
		gv := a.Version
		if a.Group != "" {
			gv = a.Group + "/" + a.Version
		}
		fmt.Fprintf(w, "  %s %s\t%s\n", gv, a.Resource, red("removed in "+a.RemovedIn))
	}
	w.Flush()
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
	scanCmd.Flags().BoolVar(&enforce, "enforce", false, "Fail the pipeline based on the operator's decision (env: MIROPS_ENFORCE)")
	scanCmd.Flags().StringVar(&enforceLevel, "enforce-level", "critical", "Minimum level that fails --enforce: critical | warning (env: MIROPS_ENFORCE_LEVEL)")
	scanCmd.Flags().StringVar(&targetVersion, "target-version", "", "Expected target version to validate (env: MIROPS_TARGET_VERSION)")
	scanCmd.Flags().StringVar(&output, "output", "table", "Output format: table, json (env: MIROPS_OUTPUT)")
	scanCmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Request timeout (env: MIROPS_TIMEOUT)")
	scanCmd.Flags().IntVar(&retry, "retry", 3, "Number of retries on failure (env: MIROPS_RETRY)")
}
