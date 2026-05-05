package report

type Scores struct {
	Total     int `json:"total"`
	Health    int `json:"health"`
	Capacity  int `json:"capacity"`
	Stability int `json:"stability"`
	Risk      int `json:"risk"`
}

type Decision struct {
	Threshold int    `json:"threshold"`
	Allow     bool   `json:"allow"`
	Level     string `json:"level"`
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
}

// Analyze evaluates the report against the given threshold.
// If thresholdOverride is 0, the report's own threshold is used.
// Returns: blocked, level (BLOCK/WARNING/SAFE), effectiveThreshold
func (r *Report) Analyze(thresholdOverride int) (blocked bool, level string, effectiveThreshold int) {
	effectiveThreshold = thresholdOverride
	if effectiveThreshold == 0 && r.Decision.Threshold > 0 {
		effectiveThreshold = r.Decision.Threshold
	}
	switch {
	case r.Scores.Total < effectiveThreshold:
		blocked = true
		level = "BLOCK"
	case r.Scores.Total < 90:
		blocked = false
		level = "WARNING"
	default:
		blocked = false
		level = "SAFE"
	}
	return
}
