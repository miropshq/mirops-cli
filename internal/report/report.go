package report

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

// Analyze evaluates the report against the given threshold.
// If thresholdOverride is 0, the report's own threshold is used.
func (r *Report) Analyze(thresholdOverride int) (blocked bool, effectiveThreshold int) {
	effectiveThreshold = thresholdOverride
	if effectiveThreshold == 0 && r.Threshold > 0 {
		effectiveThreshold = r.Threshold
	}
	blocked = r.RiskScore > effectiveThreshold
	return
}
