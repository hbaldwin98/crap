package analysis

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/hbaldwin98/crap/internal/reportcontract"
)

const ChangeScopeSchemaVersion = "1"

type ChangeScopeReport struct {
	SchemaVersion string                      `json:"schemaVersion"`
	ReportType    string                      `json:"reportType"`
	Tool          reportcontract.ToolIdentity `json:"tool"`
	Fingerprints  reportcontract.Fingerprints `json:"fingerprints"`
	Coordinates   reportcontract.Coordinates  `json:"coordinates"`
	Grammars      []GrammarIdentity           `json:"grammars"`
	Mode          string                      `json:"mode"`
	DiffBase      string                      `json:"diffBase"`
	BaseCommit    string                      `json:"baseCommit"`
	HeadCommit    string                      `json:"headCommit"`
	MergeBase     string                      `json:"mergeBase"`
	Threshold     float64                     `json:"threshold"`
	Summary       ChangeScopeSummary          `json:"summary"`
	Files         []ChangeScopeFile           `json:"files"`
	Callables     []MethodResult              `json:"callables"`
	Edges         []ChangeScopeEdge           `json:"edges"`
	Seeds         []ChangeScopeSeed           `json:"seeds"`
	Limitations   []string                    `json:"limitations"`
	Diagnostics   []Diagnostic                `json:"diagnostics"`
}

type ChangeScopeSummary struct {
	ChangedFiles     int  `json:"changedFiles"`
	ChangedCallables int  `json:"changedCallables"`
	Edges            int  `json:"edges"`
	Truncated        bool `json:"truncated"`
	OmittedNodes     int  `json:"omittedNodes"`
	OmittedEdges     int  `json:"omittedEdges"`
}

type ChangeScopeFile struct {
	ID     string             `json:"id"`
	Path   string             `json:"path"`
	Ranges []ChangeScopeRange `json:"ranges"`
}

type ChangeScopeRange struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine"`
}

type ChangeScopeEdge struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

type ChangeScopeSeed struct {
	NodeID     string `json:"nodeId"`
	Kind       string `json:"kind"`
	Provenance string `json:"provenance"`
}

func (analyzer *Analyzer) AnalyzeChangeScope(options Options) (ChangeScopeReport, error) {
	return analyzer.AnalyzeChangeScopeContext(context.Background(), options)
}

func (analyzer *Analyzer) AnalyzeChangeScopeContext(ctx context.Context, options Options) (ChangeScopeReport, error) {
	if options.DiffBase == "" {
		return ChangeScopeReport{}, fmt.Errorf("diff base is required for actual change scope")
	}
	report, inputs, err := analyzer.analyzeContext(ctx, options)
	if err != nil {
		return ChangeScopeReport{}, err
	}
	scope := ChangeScopeReport{
		SchemaVersion: ChangeScopeSchemaVersion,
		ReportType:    "change-scope",
		Tool:          report.Tool,
		Fingerprints:  report.Fingerprints,
		Coordinates:   report.Coordinates,
		Grammars:      append([]GrammarIdentity(nil), report.Grammars...),
		Mode:          "actual",
		DiffBase:      report.DiffBase,
		BaseCommit:    report.DiffBaseCommit,
		HeadCommit:    report.DiffHeadCommit,
		MergeBase:     report.DiffMergeBase,
		Threshold:     report.Threshold,
		Callables:     append([]MethodResult(nil), report.Methods...),
		Edges:         make([]ChangeScopeEdge, 0, len(report.Methods)),
		Seeds:         make([]ChangeScopeSeed, 0, len(report.Methods)),
		Files:         make([]ChangeScopeFile, 0),
		Limitations: []string{
			"relationships are limited to file containment",
			"deleted source and deleted callables are not modeled",
			"Git metadata, changed ranges, and source bytes are captured through separate reads; concurrent repository changes can make them inconsistent",
			"listed scope does not prove behavioral impact or that unlisted code is unaffected",
		},
		Diagnostics: append(make([]Diagnostic, 0, len(report.Diagnostics)), report.Diagnostics...),
	}
	scope.Fingerprints.ConfigSHA256 = reportcontract.JSONFingerprint(struct {
		Contract       string `json:"contract"`
		AnalysisConfig string `json:"analysisConfig"`
	}{ChangeScopeSchemaVersion, report.Fingerprints.ConfigSHA256})

	fileIDs := make(map[string]string)
	for _, sourcePath := range inputs.files {
		ranges := inputs.changes.ranges(sourcePath)
		if len(ranges) == 0 {
			continue
		}
		relative, err := filepath.Rel(inputs.root, sourcePath)
		if err != nil {
			return ChangeScopeReport{}, fmt.Errorf("make change scope path relative: %w", err)
		}
		path := normalizePath(relative)
		fileID := reportcontract.Fingerprint("change-scope-file-v1", path)
		fileIDs[path] = fileID
		file := ChangeScopeFile{ID: fileID, Path: path, Ranges: make([]ChangeScopeRange, len(ranges))}
		for rangeIndex, changed := range ranges {
			file.Ranges[rangeIndex] = ChangeScopeRange{StartLine: changed.Start, EndLine: changed.End}
		}
		scope.Files = append(scope.Files, file)
		scope.Seeds = append(scope.Seeds, ChangeScopeSeed{NodeID: fileID, Kind: "file", Provenance: "git-diff"})
	}
	for _, callable := range scope.Callables {
		fileID := fileIDs[callable.File]
		if fileID == "" {
			return ChangeScopeReport{}, fmt.Errorf("changed callable %s has no changed file", callable.ID)
		}
		edge := ChangeScopeEdge{
			Type: "contains",
			From: fileID,
			To:   callable.ID,
		}
		edge.ID = reportcontract.Fingerprint("change-scope-edge-v1", edge.Type, edge.From, edge.To)
		scope.Edges = append(scope.Edges, edge)
		scope.Seeds = append(scope.Seeds, ChangeScopeSeed{NodeID: callable.ID, Kind: "callable", Provenance: "git-diff-intersection"})
	}
	sort.Slice(scope.Files, func(i, j int) bool { return scope.Files[i].Path < scope.Files[j].Path })
	sort.Slice(scope.Edges, func(i, j int) bool { return scope.Edges[i].ID < scope.Edges[j].ID })
	sort.Slice(scope.Seeds, func(i, j int) bool {
		if scope.Seeds[i].Kind != scope.Seeds[j].Kind {
			return scope.Seeds[i].Kind < scope.Seeds[j].Kind
		}
		return scope.Seeds[i].NodeID < scope.Seeds[j].NodeID
	})
	scope.Summary = ChangeScopeSummary{ChangedFiles: len(scope.Files), ChangedCallables: len(scope.Callables), Edges: len(scope.Edges)}
	return scope, nil
}
