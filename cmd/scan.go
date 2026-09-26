package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/miropshq/mirops-cli/internal/providers"
	"github.com/miropshq/mirops-cli/internal/report"
	"github.com/spf13/cobra"
)

// scanSchemaVersion versions the --output json shape. Bump it only for a breaking change; adding an
// optional field doesn't need a bump.
const scanSchemaVersion = 1

var (
	source        string
	apiURL        string
	apiToken      string
	cluster       string
	enforce       bool
	enforceLevel  string
	upgradeSource string
	checkUpgrade  bool
	file          string
	namespaces    []string
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
	Short: "Gate a pipeline on the live cluster mirror",
	Long: `Evaluate a change against the live cluster mirror. The inputs you give pick the checks:

  --upgrade / MIROPS_UPGRADE=true    upgrade check, off by default. Gates on the UpgradeAnalysis report at
                                     --upgrade-source / MIROPS_UPGRADE_SOURCE; the target version comes from
                                     that report, and the check skips itself once the cluster runs it.
  -n / --namespace / MIROPS_NAMESPACE  the state of one namespace, a comma-separated list, or "all"
                                     (informational, never blocks).
  -f / --file / MIROPS_FILE          deploy check of the manifests you apply (mirops v0.3.0).

Point --source (MIROPS_SOURCE) at the ClusterMirror report (<name>.mirror). Pointing it at an
UpgradeAnalysis report (<name>.mirops) gates on that analysis directly. Every flag can be set as
MIROPS_<FLAG>.

Exit codes: 0 passed or nothing to check · 1 blocked (with --enforce) · 2 couldn't evaluate.`,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runScan(cmd))
	},
}

// scanOutput is the --output json document: one block per check that ran.
type scanOutput struct {
	SchemaVersion int         `json:"schemaVersion"`
	Source        string      `json:"source"`
	Mirror        *mirrorInfo `json:"mirror,omitempty"`
	Checks        scanChecks  `json:"checks"`
}

type mirrorInfo struct {
	Name           string               `json:"name"`
	GeneratedAt    string               `json:"generatedAt"`
	ClusterVersion string               `json:"clusterVersion"`
	Summary        report.MirrorSummary `json:"summary"`
}

type scanChecks struct {
	// Namespaces lists the requested namespaces; with "all", only those with something at risk.
	Namespaces []*namespaceCheck `json:"namespaces,omitempty"`
	// OtherHealthyNamespaces counts the namespaces "all" left out because nothing in them is at risk.
	OtherHealthyNamespaces int           `json:"otherHealthyNamespaces,omitempty"`
	Upgrade                *upgradeCheck `json:"upgrade,omitempty"`
}

// upgradeCheck is the upgrade gate's result. Blocking says whether this verdict fails the pipeline under
// --enforce at the current --enforce-level, so a JSON consumer doesn't have to re-derive it.
type upgradeCheck struct {
	Status         string                      `json:"status"` // evaluated | skipped
	Message        string                      `json:"message,omitempty"`
	Source         string                      `json:"source,omitempty"` // where the upgrade report was read
	Blocking       bool                        `json:"blocking"`
	Allow          bool                        `json:"allow"`
	Level          string                      `json:"level,omitempty"`
	Blockers       []string                    `json:"blockers,omitempty"`
	Reason         string                      `json:"reason,omitempty"`
	Cluster        string                      `json:"cluster,omitempty"`
	ClusterVersion string                      `json:"clusterVersion,omitempty"`
	TargetVersion  string                      `json:"targetVersion,omitempty"`
	Score          int                         `json:"score,omitempty"`
	AI             *report.AIScores            `json:"ai,omitempty"`
	AIReasoning    string                      `json:"aiReasoning,omitempty"`
	Issues         []string                    `json:"issues,omitempty"`
	Addons         []report.AddonCompatibility `json:"addons,omitempty"`
	Risk           *report.RiskBreakdown       `json:"risk,omitempty"`
	Graph          *report.Graph               `json:"graph,omitempty"`
	Workloads      *report.Workloads           `json:"workloads,omitempty"`

	report *report.Report // the full upgrade report, for the table view
}

// fail reports why the scan couldn't evaluate and returns exit 2 — never a pass.
func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
	return exitCannotEvaluate
}

func runScan(cmd *cobra.Command) int {
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
		for _, m := range missing {
			fmt.Fprintf(os.Stderr, "Error: SaaS mode requires %s\n", m)
		}
		if len(missing) > 0 {
			return exitCannotEvaluate
		}
		// TODO: fetch report from Mirops backend using apiURL, apiToken, cluster
		return fail("SaaS mode not yet implemented")
	}

	if output != "table" && output != "json" {
		return fail("--output must be table or json, got %q", output)
	}
	if lvl := strings.ToLower(enforceLevel); lvl != "critical" && lvl != "warning" {
		return fail("--enforce-level must be critical or warning, got %q", enforceLevel)
	}
	if source == "" {
		return fail("--source is required (or set MIROPS_SOURCE) — point it at the ClusterMirror report, e.g. s3://…/default.mirror")
	}
	if file != "" {
		return fail("deploy checks (--file / MIROPS_FILE) arrive in mirops v0.3.0; this version checks upgrades " +
			"(MIROPS_UPGRADE) and shows namespaces' state (MIROPS_NAMESPACE)")
	}
	if upgradeSource != "" && !checkUpgrade {
		fmt.Fprintln(os.Stderr, "Warning: MIROPS_UPGRADE_SOURCE is set but MIROPS_UPGRADE isn't — the upgrade check didn't run")
	}

	provider, err := providers.Validate(source)
	if err != nil {
		return fail("%v", err)
	}
	data, err := provider.Fetch(source)
	if err != nil {
		return fail("fetching source: %v", err)
	}
	kind, err := peekKind(source, data)
	if err != nil {
		return fail("%v", err)
	}

	out := scanOutput{SchemaVersion: scanSchemaVersion, Source: source}
	switch kind {
	case "", report.KindUpgradeAnalysis:
		// An upgrade report handed in directly (or from an operator before 0.2.0): pointing at an upgrade
		// report is itself the request, so it's gated on as is, switch or not.
		if len(namespaces) > 0 {
			return fail("--namespace needs the ClusterMirror report as --source; %s is an UpgradeAnalysis report", source)
		}
		if upgradeSource != "" {
			return fail("--source is already an UpgradeAnalysis report (%s); --upgrade-source goes with the ClusterMirror "+
				"report as --source", source)
		}
		r, err := parseUpgradeReport(source, data)
		if err != nil {
			return fail("%v", err)
		}
		out.Checks.Upgrade = evaluateUpgrade(r, source)

	case report.KindClusterMirror:
		var m report.MirrorReport
		if err := json.Unmarshal(data, &m); err != nil {
			return fail("invalid ClusterMirror report in %s: %v", source, err)
		}
		out.Mirror = &mirrorInfo{Name: m.Mirror, GeneratedAt: m.GeneratedAt, ClusterVersion: m.ClusterVersion, Summary: m.Summary}

		if !checkUpgrade && len(namespaces) == 0 {
			return fail("nothing to scan: set MIROPS_UPGRADE=true for the upgrade check, or MIROPS_NAMESPACE for the namespaces' state")
		}
		if checkUpgrade && upgradeSource == "" {
			return fail("MIROPS_UPGRADE=true needs MIROPS_UPGRADE_SOURCE (--upgrade-source) — the URL of the UpgradeAnalysis " +
				"report, e.g. s3://…/pre-upgrade-1.36.mirops")
		}
		if len(namespaces) > 0 {
			ns, others, err := namespacesStatus(&m, namespaces)
			if err != nil {
				return fail("%v", err)
			}
			out.Checks.Namespaces, out.Checks.OtherHealthyNamespaces = ns, others
		}
		if checkUpgrade {
			if err := requireUpgradeEnabled(&m); err != nil {
				return fail("%v", err)
			}
			r, err := fetchUpgradeReport(upgradeSource)
			if err != nil {
				return fail("%v", err)
			}
			// The mirror's cluster version is the fresher one: the analysis may predate an upgrade already done.
			if upgradePending(r.TargetVersion, m.ClusterVersion) {
				out.Checks.Upgrade = evaluateUpgrade(r, upgradeSource)
			} else {
				out.Checks.Upgrade = &upgradeCheck{Status: "skipped", Source: upgradeSource, Allow: true,
					Message: fmt.Sprintf("no upgrade pending — the analysis targets %s and the cluster already runs %s",
						r.TargetVersion, m.ClusterVersion)}
			}
		}

	default:
		return fail("%s is a %q report, which mirops scan doesn't read", source, kind)
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fail("writing JSON: %v", err)
		}
	} else {
		renderScan(out)
	}

	// Only the upgrade verdict gates; a namespace's state is information.
	if enforce && out.Checks.Upgrade != nil && out.Checks.Upgrade.Blocking {
		return exitBlocked
	}
	return exitOK
}

func parseUpgradeReport(source string, data []byte) (*report.Report, error) {
	var r report.Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("invalid UpgradeAnalysis report from %s: %w", source, err)
	}
	return &r, nil
}

// evaluateUpgrade turns the operator's verdict into the upgrade check. The gate and level come from the
// report verbatim — the operator's deterministic facts, never the score.
func evaluateUpgrade(r *report.Report, src string) *upgradeCheck {
	allow, level, blockers := r.Analyze()
	c := &upgradeCheck{
		Status:         "evaluated",
		Source:         src,
		Blocking:       !allow || report.Severity(level) >= report.Severity(enforceLevel),
		Allow:          allow,
		Level:          level,
		Blockers:       blockers,
		Reason:         r.Reason,
		Cluster:        r.Cluster,
		ClusterVersion: r.ClusterVersion,
		TargetVersion:  r.TargetVersion,
		Score:          r.Scores.Total,
		AIReasoning:    r.AIReasoning,
		Issues:         r.Issues,
		Addons:         r.Addons,
		Risk:           r.Risk,
		Graph:          r.Graph,
		Workloads:      &r.Workloads,
		report:         r,
	}
	// Emit AI scoring metadata only when AI actually ran.
	if r.Scores.AI.Ran() {
		c.AI = r.Scores.AI
	}
	return c
}

// renderScan prints the table view: the mirror header, then each check that ran.
func renderScan(out scanOutput) {
	if m := out.Mirror; m != nil {
		fmt.Printf("%s  mirror %s · cluster %s · rebuilt %s\n", boldc("MIROPS"), m.Name, m.ClusterVersion, m.GeneratedAt)
		fmt.Printf("Cluster now: %d component(s) at risk in %d of %d namespaces\n",
			m.Summary.AtRisk, m.Summary.NamespacesAtRisk, m.Summary.Namespaces)
	}
	if len(out.Checks.Namespaces) > 0 || out.Checks.OtherHealthyNamespaces > 0 {
		renderNamespaces(out.Checks.Namespaces, out.Checks.OtherHealthyNamespaces)
	}
	if up := out.Checks.Upgrade; up != nil {
		if up.Status == "skipped" {
			fmt.Println()
			fmt.Println(boldc("UPGRADE"))
			fmt.Printf("  %s %s\n", green("✔"), up.Message)
		} else {
			renderUpgrade(up)
		}
	}
}

// renderNamespaces prints the requested namespaces' current state. Always informational: it never gates.
// Healthy namespaces take one line each; with "all" they're left out and counted instead.
func renderNamespaces(list []*namespaceCheck, otherHealthy int) {
	atRisk := 0
	for _, ns := range list {
		if ns.AtRisk > 0 {
			atRisk++
		}
	}
	fmt.Println()
	fmt.Printf("%s %s\n", boldc(fmt.Sprintf("NAMESPACES — %d of %d with components at risk", atRisk, len(list)+otherHealthy)),
		dim("(informational, never blocks)"))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, ns := range list {
		if ns.AtRisk == 0 {
			fmt.Fprintf(w, "  %s %s — nothing at risk\n", green("✔"), ns.Namespace)
			continue
		}
		fmt.Fprintf(w, "  %s\t%d of %d at risk\t\t%s\n", boldc(ns.Namespace), ns.AtRisk, ns.Components, riskLabel(ns.Risk))
		for _, c := range ns.AtRiskComponents {
			st := c.Status
			if c.InheritedFrom != "" {
				st += " (inherits from " + c.InheritedFrom + ")"
			}
			fmt.Fprintf(w, "    %s/%s\t%s\t%s\t%s\n", strings.ToLower(c.Kind), c.Name, st, dependsLabel(len(c.Dependents)), riskLabel(c.Risk))
		}
	}
	if otherHealthy > 0 {
		fmt.Fprintf(w, "  %s %d other namespace(s) with nothing at risk\n", green("✔"), otherHealthy)
	}
	w.Flush()
}

func dependsLabel(n int) string {
	switch n {
	case 0:
		return "nothing depends on it"
	case 1:
		return "→ 1 depends on it"
	default:
		return fmt.Sprintf("→ %d depend on it", n)
	}
}

// riskLabel renders a 0–100 risk as its severity, colored; it is always a row's last cell.
func riskLabel(risk int) string {
	l := fmt.Sprintf("%s (%d)", report.RiskSeverity(risk), risk)
	switch {
	case risk >= 70:
		return red(l)
	case risk >= 40:
		return yellow(l)
	default:
		return green(l)
	}
}

// renderUpgrade prints the upgrade analysis and its verdict — the go/no-go the rest builds up to.
func renderUpgrade(up *upgradeCheck) {
	r := up.report
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, boldc("MIROPS UPGRADE ANALYSIS"))
	fmt.Fprintln(w, dim("─────────────────────────────────────"))
	fmt.Fprintf(w, "Cluster:\t%s\n", r.Cluster)
	fmt.Fprintf(w, "Upgrade:\t%s\n", boldc(r.ClusterVersion+"  →  "+r.TargetVersion))
	fmt.Fprintf(w, "Report:\t%s\n", up.Source)
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
	renderMirrorSummary(*r)

	// Verdict wording matches the operator/plugin: the score is a health gauge, the verdict
	// is the go/no-go. CRITICAL = blocked, WARNING = not recommended (not blocked), SAFE = allowed.
	fmt.Println()
	fmt.Println(dim("═══════════════════ VERDICT ═══════════════════"))
	switch up.Level {
	case "CRITICAL":
		fmt.Println(red("❌ Upgrade blocked"))
		for _, b := range up.Blockers {
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
	scanCmd.Flags().BoolVar(&enforce, "enforce", false, "Exit 1 when a check blocks (env: MIROPS_ENFORCE)")
	scanCmd.Flags().StringVar(&enforceLevel, "enforce-level", "critical", "Minimum level that fails --enforce: critical | warning (env: MIROPS_ENFORCE_LEVEL)")
	scanCmd.Flags().BoolVar(&checkUpgrade, "upgrade", false, "Run the upgrade check against the report at --upgrade-source (env: MIROPS_UPGRADE)")
	scanCmd.Flags().StringVar(&upgradeSource, "upgrade-source", "", "UpgradeAnalysis report to gate on: path, file://, s3://, azure://, http:// (env: MIROPS_UPGRADE_SOURCE)")
	scanCmd.Flags().StringSliceVarP(&namespaces, "namespace", "n", nil, "Namespaces to show the state of: one, a comma-separated list, or \"all\" (env: MIROPS_NAMESPACE)")
	scanCmd.Flags().StringVarP(&file, "file", "f", "", "Manifests to check before deploying — arrives in v0.3.0 (env: MIROPS_FILE)")
	scanCmd.Flags().StringVar(&output, "output", "table", "Output format: table, json (env: MIROPS_OUTPUT)")
	scanCmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Request timeout (env: MIROPS_TIMEOUT)")
	scanCmd.Flags().IntVar(&retry, "retry", 3, "Number of retries on failure (env: MIROPS_RETRY)")
}
