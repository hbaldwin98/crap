package analysis

import (
	"github.com/hbaldwin98/crap/internal/reportcontract"
	"github.com/hbaldwin98/crap/internal/rootauth"
)

const SchemaVersion = "6"

type Options struct {
	Paths            []string
	CoveragePath     string
	DiffBase         string
	Root             string
	CRAPThreshold    float64
	IncludeTests     bool
	IncludeGenerated bool
	Exclude          []string
	StrictCoverage   bool
	Authorization    *rootauth.Root
}

type Report struct {
	SchemaVersion  string                      `json:"schemaVersion"`
	ReportType     string                      `json:"reportType"`
	Tool           reportcontract.ToolIdentity `json:"tool"`
	Fingerprints   reportcontract.Fingerprints `json:"fingerprints"`
	Coordinates    reportcontract.Coordinates  `json:"coordinates"`
	Grammars       []GrammarIdentity           `json:"grammars"`
	Mode           string                      `json:"mode"`
	Coverage       CoverageMetadata            `json:"coverage"`
	Discovery      DiscoveryMetadata           `json:"discovery"`
	DiffBase       string                      `json:"diffBase,omitempty"`
	DiffBaseCommit string                      `json:"diffBaseCommit,omitempty"`
	DiffHeadCommit string                      `json:"diffHeadCommit,omitempty"`
	DiffMergeBase  string                      `json:"diffMergeBase,omitempty"`
	Threshold      float64                     `json:"threshold"`
	Summary        Summary                     `json:"summary"`
	Methods        []MethodResult              `json:"methods"`
	Diagnostics    []Diagnostic                `json:"diagnostics"`
}

type DiscoveryMetadata struct {
	Selected   int                  `json:"selected"`
	Exclusions []DiscoveryExclusion `json:"exclusions"`
}

type DiscoveryExclusion struct {
	Reason   string   `json:"reason"`
	Count    int      `json:"count"`
	Examples []string `json:"examples"`
}

type CoverageMetadata struct {
	Format        string `json:"format"`
	DisplayedPath string `json:"displayedPath,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
}

type GrammarIdentity struct {
	Language string `json:"language"`
	Version  string `json:"version"`
}

type Diagnostic struct {
	Severity   string   `json:"severity"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Path       string   `json:"path,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
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
	Signature       string   `json:"signature"`
	StartLine       int      `json:"startLine"`
	StartColumn     int      `json:"startColumn"`
	EndLine         int      `json:"endLine"`
	EndColumn       int      `json:"endColumn"`
	Complexity      int      `json:"complexity"`
	CoveragePercent *float64 `json:"coveragePercent"`
	CRAP            float64  `json:"crap"`
	Changed         bool     `json:"changed"`
	AboveThreshold  bool     `json:"aboveThreshold"`
}
