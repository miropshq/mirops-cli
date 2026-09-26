package report

// Report kinds written by the mirops operator (0.2.0+) in the report's top-level "kind". A report
// without a kind comes from an older operator and is an UpgradeAnalysis report.
const (
	KindUpgradeAnalysis = "UpgradeAnalysis"
	KindClusterMirror   = "ClusterMirror"
)

// MirrorReport is the part of the operator's ClusterMirror report (<name>.mirror) the CLI reads. The
// mirror report always exists while the operator runs, so it is the single --source every pipeline
// points at: it carries the cluster's current state and, for upgrades, whether upgrade analysis is on
// and where each analysis's report is.
type MirrorReport struct {
	Kind           string         `json:"kind"`
	GeneratedAt    string         `json:"generatedAt"`
	Mirror         string         `json:"mirror"`
	ClusterVersion string         `json:"clusterVersion"`
	Summary        MirrorSummary  `json:"summary"`
	Upgrade        MirrorUpgrade  `json:"upgrade"`
	AtRisk         []AtRiskEntry  `json:"atRisk,omitempty"`
	Risk           *RiskBreakdown `json:"risk,omitempty"`
}

// MirrorSummary is the cluster's current state in numbers (informational — it never gates).
type MirrorSummary struct {
	Components       int `json:"components"`
	AtRisk           int `json:"atRisk"`
	Namespaces       int `json:"namespaces"`
	NamespacesAtRisk int `json:"namespacesAtRisk"`
}

// MirrorUpgrade mirrors the Helm upgrade.enabled setting. Enabled is always present in the report.
type MirrorUpgrade struct {
	Enabled  bool                     `json:"enabled"`
	Analyses []UpgradeAnalysisSummary `json:"analyses,omitempty"`
	// Error is set when upgrade analysis is on but the operator couldn't list the analyses.
	Error string `json:"error,omitempty"`
}

// UpgradeAnalysisSummary is one UpgradeAnalysis as of the mirror's last rebuild.
type UpgradeAnalysisSummary struct {
	Name          string `json:"name"`
	TargetVersion string `json:"targetVersion"`
	Decision      string `json:"decision,omitempty"`
	Score         int    `json:"score"`
	// Report is the analysis report's file name, served next to the mirror report.
	Report string `json:"report"`
}

// AtRiskEntry is a component at risk in the mirror, with where its risk comes from and what depends on
// it (component ids like "Deployment/payments/payments-api").
type AtRiskEntry struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Namespace     string   `json:"namespace,omitempty"`
	Status        string   `json:"status,omitempty"`
	Risk          int      `json:"risk"`
	InheritedFrom string   `json:"inheritedFrom,omitempty"`
	Dependents    []string `json:"dependents,omitempty"`
}
