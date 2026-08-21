package analysis

const SchemaVersion = "1"

type Options struct {
	Paths         []string
	CoveragePath  string
	DiffBase      string
	Root          string
	CRAPThreshold float64
	IncludeTests  bool
}

type Report struct {
	SchemaVersion string         `json:"schemaVersion"`
	Mode          string         `json:"mode"`
	Coverage      string         `json:"coverage,omitempty"`
	DiffBase      string         `json:"diffBase,omitempty"`
	Threshold     float64        `json:"threshold"`
	Summary       Summary        `json:"summary"`
	Methods       []MethodResult `json:"methods"`
}

type Summary struct {
	Files             int     `json:"files"`
	Methods           int     `json:"methods"`
	ChangedMethods    int     `json:"changedMethods"`
	AboveThreshold    int     `json:"aboveThreshold"`
	AverageComplexity float64 `json:"averageComplexity"`
	AverageCRAP       float64 `json:"averageCrap"`
	MaximumCRAP       float64 `json:"maximumCrap"`
}

type MethodResult struct {
	ID              string   `json:"id"`
	Language        string   `json:"language"`
	File            string   `json:"file"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	StartLine       int      `json:"startLine"`
	EndLine         int      `json:"endLine"`
	Complexity      int      `json:"complexity"`
	CoveragePercent *float64 `json:"coveragePercent"`
	CRAP            float64  `json:"crap"`
	Changed         bool     `json:"changed"`
	AboveThreshold  bool     `json:"aboveThreshold"`
}
