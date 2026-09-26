package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/miropshq/mirops-cli/internal/providers"
	"github.com/miropshq/mirops-cli/internal/report"
)

// Exit codes are a fixed contract for pipelines.
const (
	exitOK             = 0 // passed, or nothing to gate on purpose (no upgrade pending)
	exitBlocked        = 1 // evaluated and blocked, with --enforce
	exitCannotEvaluate = 2 // couldn't evaluate — bad input, unreadable report, upgrade analysis off; never a pass
)

// allNamespaces selects every namespace in the mirror (MIROPS_NAMESPACE=all).
const allNamespaces = "all"

// enableUpgradeHint is how to turn upgrade analysis on; release and namespace vary per install.
const enableUpgradeHint = "helm upgrade <release> oci://ghcr.io/miropshq/charts/mirops -n <namespace> " +
	"--reuse-values --set upgrade.enabled=true"

// peekKind reads a report's top-level "kind" without committing to a shape. Empty means a report from an
// operator before 0.2.0, which is an UpgradeAnalysis report.
func peekKind(source string, data []byte) (string, error) {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", fmt.Errorf("invalid JSON in %s: %w", source, err)
	}
	return head.Kind, nil
}

// requireUpgradeEnabled stops the upgrade check when the mirror says upgrade analysis is off in the
// cluster: an analysis report left over from when it was on would be a stale verdict.
func requireUpgradeEnabled(m *report.MirrorReport) error {
	if m.Upgrade.Enabled {
		return nil
	}
	return fmt.Errorf("the upgrade check was requested (MIROPS_UPGRADE) but upgrade analysis is "+
		"disabled in this cluster (Helm upgrade.enabled=false), so there is no verdict to gate on.\n"+
		"Enable it in the mirops install:\n  %s", enableUpgradeHint)
}

// fetchUpgradeReport reads the UpgradeAnalysis report at MIROPS_UPGRADE_SOURCE, with the pipeline's own
// credentials for its scheme.
func fetchUpgradeReport(src string) (*report.Report, error) {
	provider, err := providers.Validate(src)
	if err != nil {
		return nil, err
	}
	// The provider's error already names the URL or path, so it isn't repeated here.
	data, err := provider.Fetch(src)
	if err != nil {
		return nil, fmt.Errorf("fetching the upgrade report: %w", err)
	}
	kind, err := peekKind(src, data)
	if err != nil {
		return nil, err
	}
	if kind != "" && kind != report.KindUpgradeAnalysis {
		return nil, fmt.Errorf("MIROPS_UPGRADE_SOURCE (%s) is a %q report; point it at an UpgradeAnalysis report (<name>.mirops)",
			src, kind)
	}
	return parseUpgradeReport(src, data)
}

// upgradePending reports whether the analysis targets a version above the cluster's — an upgrade still
// ahead. A version that doesn't parse counts as pending, so a malformed one fails loudly in the gate
// instead of being skipped as "already done".
func upgradePending(targetVersion, clusterVersion string) bool {
	cmp, err := compareMinor(targetVersion, clusterVersion)
	return err != nil || cmp > 0
}

// compareMinor compares two Kubernetes versions by major.minor ("v1.35.1" == "1.35"): -1, 0 or 1.
func compareMinor(a, b string) (int, error) {
	aMaj, aMin, err := majorMinor(a)
	if err != nil {
		return 0, err
	}
	bMaj, bMin, err := majorMinor(b)
	if err != nil {
		return 0, err
	}
	if aMaj != bMaj {
		return sign(aMaj - bMaj), nil
	}
	return sign(aMin - bMin), nil
}

func majorMinor(v string) (int, int, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid Kubernetes version %q (expected something like 1.36 or v1.36.2)", v)
	}
	maj, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("invalid Kubernetes version %q (expected something like 1.36 or v1.36.2)", v)
	}
	return maj, minor, nil
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// namespaceCheck is one namespace's current state from the mirror — informational, it never gates.
type namespaceCheck struct {
	Namespace        string               `json:"namespace"`
	Risk             int                  `json:"risk"`
	Components       int                  `json:"components"`
	AtRisk           int                  `json:"atRisk"`
	AtRiskComponents []report.AtRiskEntry `json:"atRiskComponents,omitempty"`
}

// namespacesStatus reads the requested namespaces' state from the mirror report. "all" returns only the
// namespaces with something at risk, worst first, plus how many healthy ones it left out — a cluster with
// 80 namespaces shouldn't print 80 rows. Named namespaces are all returned, healthy ones too, and a name
// the mirror doesn't know is an error: a typo must not look like "nothing at risk".
func namespacesStatus(m *report.MirrorReport, names []string) ([]*namespaceCheck, int, error) {
	var byNS []report.NamespaceRisk
	if m.Risk != nil {
		byNS = m.Risk.ByNamespace
	}
	build := func(n report.NamespaceRisk) *namespaceCheck {
		c := &namespaceCheck{Namespace: n.Namespace, Risk: n.Risk, Components: n.Components, AtRisk: n.AtRisk}
		for _, e := range m.AtRisk {
			if e.Namespace == n.Namespace || (e.Namespace == "" && n.Namespace == "cluster-scoped") {
				c.AtRiskComponents = append(c.AtRiskComponents, e)
			}
		}
		return c
	}

	for _, name := range names {
		if name != allNamespaces {
			continue
		}
		var out []*namespaceCheck
		for _, n := range byNS {
			if n.AtRisk > 0 {
				out = append(out, build(n))
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Risk > out[j].Risk })
		return out, len(byNS) - len(out), nil
	}

	index := make(map[string]report.NamespaceRisk, len(byNS))
	for _, n := range byNS {
		index[n.Namespace] = n
	}
	var out []*namespaceCheck
	var unknown []string
	for _, name := range names {
		n, ok := index[name]
		if !ok {
			unknown = append(unknown, fmt.Sprintf("%q", name))
			continue
		}
		out = append(out, build(n))
	}
	if len(unknown) > 0 {
		return nil, 0, fmt.Errorf("namespace %s isn't in mirror %q — it doesn't exist, or the mirror's scope leaves it out",
			strings.Join(unknown, ", "), m.Mirror)
	}
	return out, 0, nil
}
