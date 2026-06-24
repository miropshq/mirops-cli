package report

import "strings"

type Scores struct {
	Total int        `json:"total"`
	Base  *BaseScore `json:"base,omitempty"`
	AI    *AIScores  `json:"ai,omitempty"`
}

// BaseScore holds the non-AI scoring breakdown (the values are post-weight
// contributions that sum to Score).
type BaseScore struct {
	Score        int    `json:"score"`
	Weight       string `json:"weight"` // e.g. "100%"
	Contribution int    `json:"contribution"`
	Health       int    `json:"health"`
	Capacity     int    `json:"capacity"`
	Stability    int    `json:"stability"`
	Risk         int    `json:"risk"`
}

// AIScores holds the AI-assisted scoring; present only when AI scoring ran.
type AIScores struct {
	Score        int    `json:"score"`
	Weight       string `json:"weight"` // e.g. "30%"
	Contribution int    `json:"contribution"`
	Model        string `json:"model,omitempty"` // model used (e.g. claude-sonnet-4-6)
}

// Ran reports whether AI scoring actually produced a result.
func (a *AIScores) Ran() bool {
	return a != nil && (a.Weight != "" || a.Score != 0 || a.Model != "")
}

type Decision struct {
	Allow    bool     `json:"allow"`              // THE gate: true = upgrade allowed
	Level    string   `json:"level"`              // SAFE | WARNING | CRITICAL
	Blockers []string `json:"blockers,omitempty"` // every reason the upgrade is CRITICAL
}

type Report struct {
	GeneratedAt    string   `json:"generatedAt"`
	Cluster        string   `json:"cluster"`
	ClusterVersion string   `json:"clusterVersion"`
	TargetVersion  string   `json:"targetVersion"`
	Scores         Scores   `json:"scores"`
	Decision       Decision `json:"decision"`
	Reason         string   `json:"reason"`
	Issues         []string `json:"issues"`
	AIReasoning    string   `json:"aiReasoning,omitempty"` // AI explanation; empty if AI off/failed

	// Logical mirror of the cluster (additive, absent on older operators).
	Addons []AddonCompatibility `json:"addons,omitempty"`
	Graph  *Graph               `json:"graph,omitempty"`
	Risk   *RiskBreakdown       `json:"risk,omitempty"`
}

// AddonCompatibility — one per detected add-on.
type AddonCompatibility struct {
	Name            string `json:"name"`
	Version         string `json:"version,omitempty"`
	Status          string `json:"status"`                    // "compatible" | "incompatible" | "unknown"
	RequiredVersion string `json:"requiredVersion,omitempty"` // add-on version to upgrade TO (when incompatible)
	Note            string `json:"note,omitempty"`
}

type RiskBreakdown struct {
	ByNamespace []NamespaceRisk `json:"byNamespace,omitempty"`
}

type NamespaceRisk struct {
	Namespace  string `json:"namespace"` // real namespace, "default", or "cluster-scoped"
	Risk       int    `json:"risk"`      // 0-100, HIGHER = WORSE (max component risk in ns)
	Components int    `json:"components"`
	AtRisk     int    `json:"atRisk"` // components with risk >= 50
}

type Graph struct {
	Nodes []Component `json:"nodes"`
	Edges []Edge      `json:"edges"`
}

type Component struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Type      string `json:"type"` // workload|network|addon|config|storage|infra
	Version   string `json:"version,omitempty"`
	Status    string `json:"status,omitempty"`
	Risk      int    `json:"risk"` // 0-100, HIGHER = WORSE (already propagated)
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // routes-to|selects|uses-config|uses-storage|runs-on|depends-on
}

// RiskSeverity maps a risk score (0-100, HIGHER = WORSE) to a severity label.
// Scale: 0 None | 1-39 Low | 40-69 Medium | 70-89 High | 90-100 Critical.
func RiskSeverity(risk int) string {
	switch {
	case risk <= 0:
		return "None"
	case risk < 40:
		return "Low"
	case risk < 70:
		return "Medium"
	case risk < 90:
		return "High"
	default:
		return "Critical"
	}
}

// Analyze returns the operator's decision verbatim. The gate and level come from
// the report (deterministic facts computed by the operator), NOT from the score —
// scores.total is a readiness gauge only and never blocks.
func (r *Report) Analyze() (allow bool, level string, blockers []string) {
	return r.Decision.Allow, r.Decision.Level, r.Decision.Blockers
}

// Severity ranks a decision level for comparison (higher = worse).
// Case-insensitive; unknown/empty levels rank as SAFE.
func Severity(level string) int {
	switch strings.ToUpper(level) {
	case "CRITICAL":
		return 2
	case "WARNING":
		return 1
	default:
		return 0
	}
}
