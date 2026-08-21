package mutation

import "github.com/hbaldwin98/crap/internal/rootauth"

const SchemaVersion = "2"

type Options struct {
	Root           string
	Language       string
	Paths          []string
	MinimumScore   float64
	TimeoutSeconds int
	Incremental    bool
	ReportPath     string
	Authorization  *rootauth.Root
}

type Report struct {
	SchemaVersion string         `json:"schemaVersion"`
	Language      string         `json:"language"`
	Engine        string         `json:"engine"`
	Score         *float64       `json:"score"`
	ScoreSource   string         `json:"scoreSource"`
	MinimumScore  float64        `json:"minimumScore"`
	Passed        bool           `json:"passed"`
	Summary       Summary        `json:"summary"`
	Mutants       []MutantResult `json:"mutants"`
	Provenance    Provenance     `json:"provenance"`
}

type Provenance struct {
	NativeExitCode            int      `json:"nativeExitCode"`
	NativeReportSchemaVersion *string  `json:"nativeReportSchemaVersion"`
	NativeReportSHA256        *string  `json:"nativeReportSha256"`
	NativeBreakThreshold      *float64 `json:"nativeBreakThreshold"`
}

type Summary struct {
	Total        int `json:"total"`
	Killed       int `json:"killed"`
	Survived     int `json:"survived"`
	TimedOut     int `json:"timedOut"`
	NoCoverage   int `json:"noCoverage"`
	CompileError int `json:"compileError"`
	RuntimeError int `json:"runtimeError"`
	Ignored      int `json:"ignored"`
	Other        int `json:"other"`
}

type MutantResult struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Mutator     string `json:"mutator"`
	Status      string `json:"status"`
	Replacement string `json:"replacement,omitempty"`
	Reason      string `json:"reason,omitempty"`
}
