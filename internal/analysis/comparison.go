package analysis

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/reportcontract"
)

const ChangeScopeComparisonSchemaVersion = "1"

type ComparisonOptions struct {
	Analysis         Options
	BaseRevision     string
	BaseCoveragePath string
}

type ChangeScopeComparisonReport struct {
	SchemaVersion string                      `json:"schemaVersion"`
	ReportType    string                      `json:"reportType"`
	Tool          reportcontract.ToolIdentity `json:"tool"`
	Coordinates   reportcontract.Coordinates  `json:"coordinates"`
	Grammars      []GrammarIdentity           `json:"grammars"`
	BaseRevision  string                      `json:"baseRevision"`
	BaseCommit    string                      `json:"baseCommit"`
	HeadCommit    string                      `json:"headCommit"`
	MergeBase     string                      `json:"mergeBase"`
	Threshold     float64                     `json:"threshold"`
	ConfigSHA256  string                      `json:"configSha256"`
	Policy        ComparisonPolicy            `json:"policy"`
	Baseline      ComparisonRevision          `json:"baseline"`
	Current       ComparisonRevision          `json:"current"`
	Summary       ComparisonSummary           `json:"summary"`
	Callables     []CallableComparison        `json:"callables"`
	Limitations   []string                    `json:"limitations"`
	Diagnostics   []ComparisonDiagnostic      `json:"diagnostics"`
}

type ComparisonPolicy struct {
	DeltaDirection      string `json:"deltaDirection"`
	ExactMatch          string `json:"exactMatch"`
	MoveMatch           string `json:"moveMatch"`
	NewRegressionPolicy string `json:"newRegressionPolicy"`
}

type ComparisonRevision struct {
	Commit    string                           `json:"commit,omitempty"`
	Sources   []reportcontract.FileFingerprint `json:"sources"`
	Coverage  CoverageMetadata                 `json:"coverage"`
	Discovery DiscoveryMetadata                `json:"discovery"`
	Methods   int                              `json:"methods"`
}

type ComparisonSummary struct {
	Matched        int  `json:"matched"`
	Added          int  `json:"added"`
	Removed        int  `json:"removed"`
	Ambiguous      int  `json:"ambiguous"`
	NewRegressions int  `json:"newRegressions"`
	Complete       bool `json:"complete"`
}

type ComparedCallable struct {
	Method        MethodResult `json:"method"`
	CoverageState string       `json:"coverageState"`
	ContentSHA256 string       `json:"contentSha256"`
}

type CallableDelta struct {
	Complexity      int      `json:"complexity"`
	CoveragePercent *float64 `json:"coveragePercent"`
	CRAP            *float64 `json:"crap"`
	ScoreComparable bool     `json:"scoreComparable"`
}

type CallableComparison struct {
	ID            string             `json:"id"`
	Status        string             `json:"status"`
	MatchStrategy string             `json:"matchStrategy"`
	Change        string             `json:"change"`
	Baseline      []ComparedCallable `json:"baseline"`
	Current       []ComparedCallable `json:"current"`
	Delta         *CallableDelta     `json:"delta"`
	NewRegression bool               `json:"newRegression"`
	Reasons       []string           `json:"reasons"`
}

type ComparisonDiagnostic struct {
	Revision   string     `json:"revision"`
	Diagnostic Diagnostic `json:"diagnostic"`
}

func (analyzer *Analyzer) CompareChangeScope(options ComparisonOptions) (ChangeScopeComparisonReport, error) {
	return analyzer.CompareChangeScopeContext(context.Background(), options)
}

func (analyzer *Analyzer) CompareChangeScopeContext(ctx context.Context, options ComparisonOptions) (ChangeScopeComparisonReport, error) {
	if strings.TrimSpace(options.BaseRevision) == "" {
		return ChangeScopeComparisonReport{}, fmt.Errorf("base revision is required")
	}
	if options.Analysis.DiffBase != "" {
		return ChangeScopeComparisonReport{}, fmt.Errorf("comparison does not accept an analysis diff base")
	}
	root, err := canonicalAnalysisRoot(options.Analysis.Root, options.Analysis.Authorization)
	if err != nil {
		return ChangeScopeComparisonReport{}, err
	}
	revision, err := prepareGitChangeRequest(ctx, root, options.BaseRevision, nil, analyzer.git, options.Analysis.Authorization)
	if err != nil {
		return ChangeScopeComparisonReport{}, err
	}
	baselineSnapshot, err := analyzer.readGitSourceSnapshot(ctx, root, revision.repositoryRoot, revision.mergeBase, options.Analysis)
	if err != nil {
		return ChangeScopeComparisonReport{}, err
	}
	currentOptions := options.Analysis
	currentOptions.Root = root
	currentOptions.DiffBase = ""
	currentOptions = comparisonCurrentOptions(root, currentOptions)
	current, currentInputs, err := analyzer.analyzeContext(ctx, currentOptions)
	if err != nil {
		return ChangeScopeComparisonReport{}, err
	}
	baselineCoverage, baselineCoverageMetadata, err := loadComparisonCoverage(ctx, root, options.BaseCoveragePath, options.Analysis.Authorization)
	if err != nil {
		return ChangeScopeComparisonReport{}, fmt.Errorf("load baseline coverage: %w", err)
	}
	baseline, err := analyzer.analyzeGitSnapshot(ctx, root, baselineSnapshot, baselineCoverage, baselineCoverageMetadata, options.Analysis)
	if err != nil {
		return ChangeScopeComparisonReport{}, err
	}
	report := newComparisonReport(root, options, revision, baseline, current)
	report.Callables, report.Summary = compareCallables(ctx, baseline, current, options.Analysis.CRAPThreshold, snapshotContents(baselineSnapshot), currentInputs.sources)
	if err := ctx.Err(); err != nil {
		return ChangeScopeComparisonReport{}, err
	}
	return report, nil
}

func newComparisonReport(root string, options ComparisonOptions, revision gitChangeRequest, baseline, current Report) ChangeScopeComparisonReport {
	grammars := append([]GrammarIdentity(nil), baseline.Grammars...)
	seen := make(map[string]bool, len(grammars))
	for _, grammar := range grammars {
		seen[grammar.Language+"\x00"+grammar.Version] = true
	}
	for _, grammar := range current.Grammars {
		key := grammar.Language + "\x00" + grammar.Version
		if !seen[key] {
			seen[key] = true
			grammars = append(grammars, grammar)
		}
	}
	sort.Slice(grammars, func(i, j int) bool {
		if grammars[i].Language == grammars[j].Language {
			return grammars[i].Version < grammars[j].Version
		}
		return grammars[i].Language < grammars[j].Language
	})
	diagnostics := make([]ComparisonDiagnostic, 0, len(baseline.Diagnostics)+len(current.Diagnostics))
	for _, diagnostic := range baseline.Diagnostics {
		diagnostics = append(diagnostics, ComparisonDiagnostic{Revision: "baseline", Diagnostic: diagnostic})
	}
	for _, diagnostic := range current.Diagnostics {
		diagnostics = append(diagnostics, ComparisonDiagnostic{Revision: "current", Diagnostic: diagnostic})
	}
	configPaths := append([]string(nil), options.Analysis.Paths...)
	if len(configPaths) == 0 {
		configPaths = []string{"."}
	}
	for index := range configPaths {
		configPaths[index] = semanticPath(root, configPaths[index])
	}
	sort.Strings(configPaths)
	excludes := append([]string(nil), options.Analysis.Exclude...)
	sort.Strings(excludes)
	excludes = slicesCompact(excludes)
	config := reportcontract.JSONFingerprint(struct {
		Contract         string   `json:"contract"`
		Paths            []string `json:"paths"`
		Threshold        float64  `json:"threshold"`
		IncludeTests     bool     `json:"includeTests"`
		IncludeGenerated bool     `json:"includeGenerated"`
		Exclude          []string `json:"exclude"`
		StrictCoverage   bool     `json:"strictCoverage"`
	}{ChangeScopeComparisonSchemaVersion, configPaths, options.Analysis.CRAPThreshold, options.Analysis.IncludeTests, options.Analysis.IncludeGenerated, excludes, options.Analysis.StrictCoverage})
	return ChangeScopeComparisonReport{
		SchemaVersion: ChangeScopeComparisonSchemaVersion,
		ReportType:    "change-scope-comparison",
		Tool:          buildinfo.Tool("crap"),
		Coordinates:   reportcontract.DefaultCoordinates(),
		Grammars:      grammars,
		BaseRevision:  options.BaseRevision,
		BaseCommit:    revision.baseCommit,
		HeadCommit:    revision.headCommit,
		MergeBase:     revision.mergeBase,
		Threshold:     options.Analysis.CRAPThreshold,
		ConfigSHA256:  config,
		Policy: ComparisonPolicy{
			DeltaDirection:      "current-minus-baseline",
			ExactMatch:          "callable-id",
			MoveMatch:           "unique-language-kind-name-signature",
			NewRegressionPolicy: "added-above-threshold-or-threshold-crossed-with-comparable-score",
		},
		Baseline:  comparisonRevision(baseline, revision.mergeBase),
		Current:   comparisonRevision(current, ""),
		Summary:   ComparisonSummary{Complete: true},
		Callables: make([]CallableComparison, 0),
		Limitations: []string{
			"path-independent matching requires a unique language, kind, name, and signature on both revisions; ambiguous candidates are never guessed",
			"renamed callables whose declaration and body also changed remain unmatched",
			"coverage and CRAP deltas are comparable only when both revisions have measured coverage or both omit coverage",
			"comparison reports structural quality changes, not semantic or behavioral impact",
		},
		Diagnostics: diagnostics,
	}
}

func comparisonRevision(report Report, commit string) ComparisonRevision {
	return ComparisonRevision{
		Commit: commit, Sources: append([]reportcontract.FileFingerprint(nil), report.Fingerprints.Sources...),
		Coverage: report.Coverage, Discovery: report.Discovery, Methods: len(report.Methods),
	}
}

func compareCallables(ctx context.Context, baseline, current Report, threshold float64, baselineContent, currentContent map[string][]byte) ([]CallableComparison, ComparisonSummary) {
	base := comparedCallables(baseline, baselineContent)
	now := comparedCallables(current, currentContent)
	baseUsed := make([]bool, len(base))
	nowUsed := make([]bool, len(now))
	comparisons := make([]CallableComparison, 0, len(base)+len(now))
	baseIDs, nowIDs := callableIndexes(base, func(callable ComparedCallable) string { return callable.Method.ID }), callableIndexes(now, func(callable ComparedCallable) string { return callable.Method.ID })
	allBaseDeclarations, allNowDeclarations := callableIndexes(base, declarationMatchKey), callableIndexes(now, declarationMatchKey)
	for _, key := range sharedUniqueKeys(baseIDs, nowIDs) {
		left, right := baseIDs[key][0], nowIDs[key][0]
		baseDeclaration, currentDeclaration := declarationMatchKey(base[left]), declarationMatchKey(now[right])
		if len(allBaseDeclarations[baseDeclaration]) != 1 || len(allNowDeclarations[currentDeclaration]) != 1 {
			continue
		}
		baseUsed[left], nowUsed[right] = true, true
		comparisons = append(comparisons, matchedComparison(base[left], now[right], "id", threshold, baseline.Coverage.Format == current.Coverage.Format))
	}
	baseDeclarations := callableIndexesRemaining(base, baseUsed, declarationMatchKey)
	nowDeclarations := callableIndexesRemaining(now, nowUsed, declarationMatchKey)
	keys := sharedKeys(baseDeclarations, nowDeclarations)
	for _, key := range keys {
		if ctx.Err() != nil {
			return nil, ComparisonSummary{}
		}
		left, right := baseDeclarations[key], nowDeclarations[key]
		if len(left) == 1 && len(right) == 1 {
			baseUsed[left[0]], nowUsed[right[0]] = true, true
			comparisons = append(comparisons, matchedComparison(base[left[0]], now[right[0]], "declaration", threshold, baseline.Coverage.Format == current.Coverage.Format))
			continue
		}
		baselineCandidates, currentCandidates := selectCompared(base, left), selectCompared(now, right)
		for _, index := range left {
			baseUsed[index] = true
		}
		for _, index := range right {
			nowUsed[index] = true
		}
		comparisons = append(comparisons, unmatchedComparison("ambiguous", baselineCandidates, currentCandidates))
	}
	for index, callable := range base {
		if !baseUsed[index] {
			comparisons = append(comparisons, unmatchedComparison("removed", []ComparedCallable{callable}, nil))
		}
	}
	for index, callable := range now {
		if !nowUsed[index] {
			comparison := unmatchedComparison("added", nil, []ComparedCallable{callable})
			if callable.Method.CRAP > threshold {
				comparison.NewRegression = true
				comparison.Reasons = []string{"added-above-threshold"}
			}
			comparisons = append(comparisons, comparison)
		}
	}
	sort.Slice(comparisons, func(i, j int) bool { return comparisons[i].ID < comparisons[j].ID })
	summary := ComparisonSummary{Complete: true}
	for _, comparison := range comparisons {
		switch comparison.Status {
		case "matched":
			summary.Matched++
		case "added":
			summary.Added++
		case "removed":
			summary.Removed++
		case "ambiguous":
			summary.Ambiguous++
			summary.Complete = false
		}
		if comparison.NewRegression {
			summary.NewRegressions++
		}
	}
	return comparisons, summary
}

func comparedCallables(report Report, content map[string][]byte) []ComparedCallable {
	diagnosticKind := make(map[string]string)
	for _, diagnostic := range report.Diagnostics {
		switch diagnostic.Code {
		case "coverage-path-unmatched":
			diagnosticKind[diagnostic.Path] = "unmatched"
		case "coverage-path-ambiguous":
			diagnosticKind[diagnostic.Path] = "ambiguous"
		}
	}
	result := make([]ComparedCallable, len(report.Methods))
	for index, method := range report.Methods {
		state := "measured"
		switch {
		case report.Coverage.Format == "none":
			state = "absent"
		case diagnosticKind[method.File] != "":
			state = diagnosticKind[method.File]
		case method.CoveragePercent == nil:
			state = "uninstrumented"
		}
		result[index] = ComparedCallable{Method: method, CoverageState: state, ContentSHA256: callableContentSHA256(method, content[method.File])}
	}
	return result
}

func matchedComparison(baseline, current ComparedCallable, strategy string, threshold float64, sameCoverageFormat bool) CallableComparison {
	coverageDelta := nullableDelta(baseline.Method.CoveragePercent, current.Method.CoveragePercent)
	comparable := sameCoverageFormat && ((baseline.CoverageState == "measured" && current.CoverageState == "measured") || (baseline.CoverageState == "absent" && current.CoverageState == "absent"))
	if !sameCoverageFormat {
		coverageDelta = nil
	}
	var crapDelta *float64
	if comparable {
		value := round(current.Method.CRAP-baseline.Method.CRAP, 2)
		crapDelta = &value
	}
	change := "unchanged"
	moved := baseline.Method.File != current.Method.File
	modified := baseline.ContentSHA256 != current.ContentSHA256 || baseline.Method.Complexity != current.Method.Complexity || baseline.Method.CRAP != current.Method.CRAP || coverageDeltaValue(coverageDelta) != 0 || baseline.Method.Signature != current.Method.Signature
	if moved && modified {
		change = "moved-modified"
	} else if moved {
		change = "moved"
	} else if modified {
		change = "modified"
	}
	reasons := make([]string, 0, 3)
	if current.Method.Complexity > baseline.Method.Complexity {
		reasons = append(reasons, "complexity-increased")
	}
	if coverageDelta != nil && *coverageDelta < 0 {
		reasons = append(reasons, "coverage-decreased")
	}
	if crapDelta != nil && *crapDelta > 0 {
		reasons = append(reasons, "crap-increased")
	}
	newRegression := comparable && !baseline.Method.AboveThreshold && current.Method.AboveThreshold
	if newRegression {
		reasons = append(reasons, "threshold-crossed")
	}
	return CallableComparison{
		ID: comparisonID("matched", []ComparedCallable{baseline}, []ComparedCallable{current}), Status: "matched", MatchStrategy: strategy, Change: change,
		Baseline: []ComparedCallable{baseline}, Current: []ComparedCallable{current},
		Delta:         &CallableDelta{Complexity: current.Method.Complexity - baseline.Method.Complexity, CoveragePercent: coverageDelta, CRAP: crapDelta, ScoreComparable: comparable},
		NewRegression: newRegression, Reasons: reasons,
	}
}

func unmatchedComparison(status string, baseline, current []ComparedCallable) CallableComparison {
	change := status
	return CallableComparison{ID: comparisonID(status, baseline, current), Status: status, MatchStrategy: "none", Change: change, Baseline: nonnilCallables(baseline), Current: nonnilCallables(current), Reasons: make([]string, 0)}
}

func comparisonID(status string, baseline, current []ComparedCallable) string {
	ids := []string{"change-scope-comparison-v1", status}
	for _, callable := range baseline {
		ids = append(ids, "baseline", callable.Method.ID)
	}
	for _, callable := range current {
		ids = append(ids, "current", callable.Method.ID)
	}
	return reportcontract.Fingerprint(ids...)
}

func declarationMatchKey(callable ComparedCallable) string {
	method := callable.Method
	return reportcontract.Fingerprint("callable-declaration-v1", method.Language, method.Kind, method.Name, method.Signature)
}

func callableIndexes(callables []ComparedCallable, key func(ComparedCallable) string) map[string][]int {
	result := make(map[string][]int)
	for index, callable := range callables {
		result[key(callable)] = append(result[key(callable)], index)
	}
	return result
}

func callableIndexesRemaining(callables []ComparedCallable, used []bool, key func(ComparedCallable) string) map[string][]int {
	result := make(map[string][]int)
	for index, callable := range callables {
		if !used[index] {
			result[key(callable)] = append(result[key(callable)], index)
		}
	}
	return result
}

func sharedUniqueKeys(left, right map[string][]int) []string {
	keys := make([]string, 0)
	for key, values := range left {
		if len(values) == 1 && len(right[key]) == 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func sharedKeys(left, right map[string][]int) []string {
	keys := make([]string, 0)
	for key := range left {
		if len(right[key]) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func selectCompared(callables []ComparedCallable, indexes []int) []ComparedCallable {
	result := make([]ComparedCallable, len(indexes))
	for index, source := range indexes {
		result[index] = callables[source]
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Method.ID < result[j].Method.ID })
	return result
}

func nonnilCallables(callables []ComparedCallable) []ComparedCallable {
	if callables == nil {
		return make([]ComparedCallable, 0)
	}
	return callables
}

func nullableDelta(baseline, current *float64) *float64 {
	if baseline == nil || current == nil {
		return nil
	}
	value := round(*current-*baseline, 2)
	return &value
}

func coverageDeltaValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func comparisonCurrentOptions(root string, options Options) Options {
	if len(options.Paths) == 0 {
		return options
	}
	paths := make([]string, 0, len(options.Paths))
	for _, value := range options.Paths {
		path := value
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		_, err := os.Stat(path)
		if err == nil || !os.IsNotExist(err) || !supportedSource(value) {
			paths = append(paths, value)
		}
	}
	if len(paths) == 0 {
		options.Paths = nil
		options.Exclude = append(append([]string(nil), options.Exclude...), "**")
		return options
	}
	options.Paths = paths
	return options
}

func snapshotContents(snapshot gitSourceSnapshot) map[string][]byte {
	contents := make(map[string][]byte, len(snapshot.files))
	for _, file := range snapshot.files {
		contents[file.path] = file.data
	}
	return contents
}

func callableContentSHA256(method MethodResult, data []byte) string {
	start, startOK := sourceCoordinateOffset(data, method.StartLine, method.StartColumn)
	end, endOK := sourceCoordinateOffset(data, method.EndLine, method.EndColumn)
	if !startOK || !endOK || end < start {
		return reportcontract.SHA256(data)
	}
	return reportcontract.SHA256(data[start:end])
}

func sourceCoordinateOffset(data []byte, line, column int) (int, bool) {
	if line < 1 || column < 1 {
		return 0, false
	}
	start := 0
	for current := 1; current < line; current++ {
		newline := bytes.IndexByte(data[start:], '\n')
		if newline < 0 {
			return 0, false
		}
		start += newline + 1
	}
	end := len(data)
	if newline := bytes.IndexByte(data[start:], '\n'); newline >= 0 {
		end = start + newline
	}
	offset := start + column - 1
	return offset, offset >= start && offset <= end
}

func loadComparisonCoverage(ctx context.Context, root, value string, authorization interface{ Existing(string) (string, error) }) (coverageData, CoverageMetadata, error) {
	path := value
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if path != "" && authorization != nil {
		var err error
		path, err = authorization.Existing(path)
		if err != nil {
			return coverageData{}, CoverageMetadata{}, fmt.Errorf("authorize coverage report: %w", err)
		}
	}
	coverage, err := loadCoverageContext(ctx, path, root)
	if err != nil {
		return coverageData{}, CoverageMetadata{}, err
	}
	metadata := CoverageMetadata{Format: "none"}
	if coverage.loaded {
		metadata = CoverageMetadata{Format: coverage.format, DisplayedPath: normalizeDisplayedPath(value), SHA256: coverage.sha256}
	}
	return coverage, metadata, nil
}
