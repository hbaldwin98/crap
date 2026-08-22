package mutation

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"

	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/reportcontract"
)

type strykerReport struct {
	SchemaVersion string `json:"schemaVersion"`
	Thresholds    *struct {
		High  *float64 `json:"high"`
		Low   *float64 `json:"low"`
		Break *float64 `json:"break"`
	} `json:"thresholds"`
	Files map[string]strykerFile `json:"files"`
}

type strykerFile struct {
	Language *string          `json:"language"`
	Source   *string          `json:"source"`
	Mutants  *[]strykerMutant `json:"mutants"`
}

type strykerMutant struct {
	ID           string           `json:"id"`
	MutatorName  string           `json:"mutatorName"`
	Replacement  string           `json:"replacement"`
	Description  string           `json:"description"`
	Status       string           `json:"status"`
	StatusReason string           `json:"statusReason"`
	Location     *strykerLocation `json:"location"`
}

type strykerLocation struct {
	Start *strykerPosition `json:"start"`
	End   *strykerPosition `json:"end"`
}

type gremlinsReport struct {
	TestEfficacy      *float64       `json:"test_efficacy"`
	MutantsTotal      *int           `json:"mutants_total"`
	MutantsKilled     *int           `json:"mutants_killed"`
	MutantsLived      *int           `json:"mutants_lived"`
	MutantsNotViable  *int           `json:"mutants_not_viable"`
	MutantsNotCovered *int           `json:"mutants_not_covered"`
	Files             []gremlinsFile `json:"files"`
}

type gremlinsFile struct {
	FileName  string            `json:"file_name"`
	Mutations *[]gremlinsMutant `json:"mutations"`
}

type gremlinsMutant struct {
	Line   *int   `json:"line"`
	Column *int   `json:"column"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

func parseStryker(data []byte, language, engine string, minimum float64) (Report, error) {
	var raw strykerReport
	if err := json.Unmarshal(data, &raw); err != nil {
		return Report{}, fmt.Errorf("parse %s JSON report: %w", engine, err)
	}
	if err := validateStrykerReport(raw, engine); err != nil {
		return Report{}, err
	}
	mutants, sources, err := parseStrykerFiles(raw.Files, engine)
	if err != nil {
		return Report{}, err
	}
	provenance := reportProvenance(data, &raw.SchemaVersion)
	provenance.NativeBreakThreshold = raw.Thresholds.Break
	report := makeReport(language, engine, "report-statuses", minimum, nil, mutants, provenance)
	report.Fingerprints.Sources = sources
	reportcontract.SortFiles(report.Fingerprints.Sources)
	report.Fingerprints.NativeReport = &reportcontract.FileFingerprint{SHA256: reportcontract.SHA256(data)}
	return report, nil
}

func validateStrykerReport(raw strykerReport, engine string) error {
	if raw.SchemaVersion == "" {
		return fmt.Errorf("parse %s JSON report: missing schemaVersion", engine)
	}
	if raw.Files == nil {
		return fmt.Errorf("parse %s JSON report: missing files", engine)
	}
	if !supportedStrykerSchema(engine, raw.SchemaVersion) {
		return fmt.Errorf("parse %s JSON report: unsupported schemaVersion %q", engine, raw.SchemaVersion)
	}
	if raw.Thresholds == nil || raw.Thresholds.High == nil || raw.Thresholds.Low == nil {
		return fmt.Errorf("parse %s JSON report: missing thresholds", engine)
	}
	if !validPercentage(*raw.Thresholds.High) || !validPercentage(*raw.Thresholds.Low) || *raw.Thresholds.High < *raw.Thresholds.Low || (raw.Thresholds.Break != nil && (!validPercentage(*raw.Thresholds.Break) || *raw.Thresholds.Break > *raw.Thresholds.Low)) {
		return fmt.Errorf("parse %s JSON report: invalid thresholds", engine)
	}
	return nil
}

func parseStrykerFiles(files map[string]strykerFile, engine string) ([]MutantResult, []reportcontract.FileFingerprint, error) {
	mutants := make([]MutantResult, 0)
	nativeIDs := make(map[string]struct{})
	wrapperIDs := make(map[string]struct{})
	sources := make([]reportcontract.FileFingerprint, 0, len(files))
	sourcePaths := make(map[string]struct{}, len(files))
	for file, result := range files {
		file = normalizeReportPath(file)
		if !validReportPath(file) {
			return nil, nil, fmt.Errorf("parse %s JSON report: invalid file path %q", engine, file)
		}
		if _, duplicate := sourcePaths[file]; duplicate {
			return nil, nil, fmt.Errorf("parse %s JSON report: duplicate normalized file path %q", engine, file)
		}
		sourcePaths[file] = struct{}{}
		if result.Language == nil || *result.Language == "" || result.Source == nil {
			return nil, nil, fmt.Errorf("parse %s JSON report: incomplete file entry for %s", engine, file)
		}
		if result.Mutants == nil {
			return nil, nil, fmt.Errorf("parse %s JSON report: missing mutants for %s", engine, file)
		}
		sources = append(sources, reportcontract.FileFingerprint{Path: file, SHA256: reportcontract.SHA256([]byte(*result.Source))})
		for _, mutant := range *result.Mutants {
			parsed, err := parseStrykerMutant(mutant, file, engine, nativeIDs, wrapperIDs)
			if err != nil {
				return nil, nil, err
			}
			mutants = append(mutants, parsed)
		}
	}
	return mutants, sources, nil
}

func parseStrykerMutant(mutant strykerMutant, file, engine string, nativeIDs, wrapperIDs map[string]struct{}) (MutantResult, error) {
	if mutant.ID == "" {
		return MutantResult{}, fmt.Errorf("parse %s JSON report: missing mutant id in %s", engine, file)
	}
	if _, duplicate := nativeIDs[mutant.ID]; duplicate {
		return MutantResult{}, fmt.Errorf("parse %s JSON report: duplicate mutant id %q", engine, mutant.ID)
	}
	nativeIDs[mutant.ID] = struct{}{}
	if mutant.MutatorName == "" {
		return MutantResult{}, fmt.Errorf("parse %s JSON report: mutant %s has no mutator name", engine, mutant.ID)
	}
	status, ok := strykerStatus(mutant.Status)
	if !ok {
		return MutantResult{}, fmt.Errorf("parse %s JSON report: mutant %s has unsupported status %q", engine, mutant.ID, mutant.Status)
	}
	if mutant.Location == nil || !completeStrykerPosition(mutant.Location.Start) || !completeStrykerPosition(mutant.Location.End) {
		return MutantResult{}, fmt.Errorf("parse %s JSON report: mutant %s has incomplete location", engine, mutant.ID)
	}
	line, column := *mutant.Location.Start.Line, *mutant.Location.Start.Column
	endLine, endColumn := *mutant.Location.End.Line, *mutant.Location.End.Column
	if line < 1 || column < 1 || endColumn < 1 || endLine < line || (endLine == line && endColumn < column) {
		return MutantResult{}, fmt.Errorf("parse %s JSON report: mutant %s has invalid location", engine, mutant.ID)
	}
	reason := mutant.StatusReason
	if reason == "" {
		reason = mutant.Description
	}
	id := mutantID(file, line, column, &endLine, &endColumn, mutant.MutatorName, mutant.Replacement)
	if _, duplicate := wrapperIDs[id]; duplicate {
		return MutantResult{}, fmt.Errorf("parse %s JSON report: duplicate normalized mutant %q", engine, id)
	}
	wrapperIDs[id] = struct{}{}
	nativeID := mutant.ID
	return MutantResult{ID: id, NativeID: &nativeID, File: file, Line: line, Column: column, StartLine: line, StartColumn: column,
		EndLine: &endLine, EndColumn: &endColumn, Mutator: mutant.MutatorName, Status: status, Replacement: mutant.Replacement, Reason: reason}, nil
}

type strykerPosition struct {
	Line   *int `json:"line"`
	Column *int `json:"column"`
}

func completeStrykerPosition(position *strykerPosition) bool {
	return position != nil && position.Line != nil && position.Column != nil
}

func parseGremlins(data []byte, minimum float64) (Report, error) {
	var raw gremlinsReport
	if err := json.Unmarshal(data, &raw); err != nil {
		return Report{}, fmt.Errorf("parse Gremlins JSON report: %w", err)
	}
	if err := validateGremlinsReport(raw); err != nil {
		return Report{}, err
	}
	mutants, err := parseGremlinsFiles(raw.Files)
	if err != nil {
		return Report{}, err
	}
	score, err := validateGremlinsSummary(raw, mutants)
	if err != nil {
		return Report{}, err
	}
	report := makeReport("go", "gremlins", "engine", minimum, &score, mutants, reportProvenance(data, nil))
	report.Fingerprints.NativeReport = &reportcontract.FileFingerprint{SHA256: reportcontract.SHA256(data)}
	return report, nil
}

func validateGremlinsReport(raw gremlinsReport) error {
	if raw.TestEfficacy == nil {
		return fmt.Errorf("parse Gremlins JSON report: missing test_efficacy")
	}
	if math.IsNaN(*raw.TestEfficacy) || math.IsInf(*raw.TestEfficacy, 0) || *raw.TestEfficacy < 0 || *raw.TestEfficacy > 100 {
		return fmt.Errorf("parse Gremlins JSON report: test_efficacy must be between 0 and 100")
	}
	if raw.Files == nil {
		return fmt.Errorf("parse Gremlins JSON report: missing files")
	}
	if raw.MutantsTotal == nil || raw.MutantsKilled == nil || raw.MutantsLived == nil || raw.MutantsNotViable == nil || raw.MutantsNotCovered == nil {
		return fmt.Errorf("parse Gremlins JSON report: missing mutant counts")
	}
	return nil
}

func parseGremlinsFiles(files []gremlinsFile) ([]MutantResult, error) {
	mutants := make([]MutantResult, 0)
	ids := make(map[string]struct{})
	for _, file := range files {
		filename := normalizeReportPath(file.FileName)
		if !validReportPath(filename) {
			return nil, fmt.Errorf("parse Gremlins JSON report: invalid file_name %q", filename)
		}
		if file.Mutations == nil {
			return nil, fmt.Errorf("parse Gremlins JSON report: missing mutations for %s", filename)
		}
		for _, mutant := range *file.Mutations {
			parsed, err := parseGremlinsMutant(mutant, filename, ids)
			if err != nil {
				return nil, err
			}
			mutants = append(mutants, parsed)
		}
	}
	return mutants, nil
}

func parseGremlinsMutant(mutant gremlinsMutant, filename string, ids map[string]struct{}) (MutantResult, error) {
	if mutant.Line == nil || mutant.Column == nil || *mutant.Line < 1 || *mutant.Column < 1 {
		return MutantResult{}, fmt.Errorf("parse Gremlins JSON report: invalid mutation location in %s", filename)
	}
	if mutant.Type == "" {
		return MutantResult{}, fmt.Errorf("parse Gremlins JSON report: missing mutation type in %s", filename)
	}
	status, ok := gremlinsStatus(mutant.Status)
	if !ok {
		return MutantResult{}, fmt.Errorf("parse Gremlins JSON report: unsupported status %q", mutant.Status)
	}
	id := mutantID(filename, *mutant.Line, *mutant.Column, nil, nil, mutant.Type, "")
	if _, duplicate := ids[id]; duplicate {
		return MutantResult{}, fmt.Errorf("parse Gremlins JSON report: duplicate mutant %q", id)
	}
	ids[id] = struct{}{}
	return MutantResult{ID: id, File: filename, Line: *mutant.Line, Column: *mutant.Column,
		StartLine: *mutant.Line, StartColumn: *mutant.Column, Mutator: mutant.Type, Status: status}, nil
}

func validateGremlinsSummary(raw gremlinsReport, mutants []MutantResult) (float64, error) {
	summary := summarize(mutants)
	if *raw.MutantsTotal != summary.Killed+summary.Survived+summary.CompileError || *raw.MutantsKilled != summary.Killed || *raw.MutantsLived != summary.Survived || *raw.MutantsNotViable != summary.CompileError || *raw.MutantsNotCovered != summary.NoCoverage {
		return 0, fmt.Errorf("parse Gremlins JSON report: mutant counts do not match files")
	}
	expected := 0.0
	if summary.Killed+summary.Survived > 0 {
		expected = round(float64(summary.Killed) / float64(summary.Killed+summary.Survived) * 100)
	}
	score := round(*raw.TestEfficacy)
	if score != expected {
		return 0, fmt.Errorf("parse Gremlins JSON report: test_efficacy %.2f does not match mutant counts %.2f", score, expected)
	}
	return score, nil
}

func makeReport(language, engine, source string, minimum float64, engineScore *float64, mutants []MutantResult, provenance Provenance) Report {
	sort.Slice(mutants, func(i, j int) bool {
		left, right := mutants[i], mutants[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return left.ID < right.ID
	})
	summary := summarize(mutants)
	score := engineScore
	if score == nil {
		detected := summary.Killed + summary.TimedOut
		scorable := detected + summary.Survived + summary.NoCoverage
		if scorable > 0 {
			value := round(float64(detected) / float64(scorable) * 100)
			score = &value
		}
	}
	return Report{
		SchemaVersion: SchemaVersion, ReportType: "mutation", Tool: buildinfo.Tool("crap-mutate"),
		Coordinates: reportcontract.NativeCoordinates(), Language: language, Engine: engine,
		EngineIdentity: EngineIdentity{Name: engine, ReportContract: engineReportContract(engine), ReportContractVersion: engineReportContractVersion(engine)},
		Fingerprints: reportcontract.Fingerprints{Sources: make([]reportcontract.FileFingerprint, 0), ConfigSHA256: reportcontract.JSONFingerprint(struct {
			Language string  `json:"language"`
			Engine   string  `json:"engine"`
			Minimum  float64 `json:"minimumScore"`
		}{language, engine, minimum})},
		Score: score, ScoreSource: source, MinimumScore: minimum,
		Passed: score != nil && *score >= minimum, Summary: summary, Mutants: mutants, Provenance: provenance,
	}
}

func mutantID(file string, startLine, startColumn int, endLine, endColumn *int, mutator, replacement string) string {
	end := "point"
	if endLine != nil && endColumn != nil {
		end = fmt.Sprintf("%d:%d", *endLine, *endColumn)
	}
	return reportcontract.Fingerprint(normalizeReportPath(file), fmt.Sprintf("%d:%d-%s", startLine, startColumn, end), mutator, replacement)
}

func engineReportContract(engine string) string {
	if engine == "gremlins" {
		return "gremlins-json"
	}
	return "stryker-mutation-testing-report"
}

func engineReportContractVersion(engine string) string {
	switch engine {
	case "gremlins":
		return "v0.6"
	case "stryker-net":
		return "2"
	default:
		return "1.0"
	}
}

func summarize(mutants []MutantResult) Summary {
	summary := Summary{Total: len(mutants)}
	for _, mutant := range mutants {
		switch mutant.Status {
		case "killed":
			summary.Killed++
		case "survived":
			summary.Survived++
		case "timedOut":
			summary.TimedOut++
		case "noCoverage":
			summary.NoCoverage++
		case "compileError":
			summary.CompileError++
		case "runtimeError":
			summary.RuntimeError++
		case "ignored":
			summary.Ignored++
		default:
			summary.Other++
		}
	}
	return summary
}

func strykerStatus(status string) (string, bool) {
	switch status {
	case "Killed":
		return "killed", true
	case "Survived":
		return "survived", true
	case "Timeout":
		return "timedOut", true
	case "NoCoverage":
		return "noCoverage", true
	case "CompileError":
		return "compileError", true
	case "RuntimeError":
		return "runtimeError", true
	case "Ignored":
		return "ignored", true
	default:
		return "", false
	}
}

func gremlinsStatus(status string) (string, bool) {
	switch status {
	case "KILLED":
		return "killed", true
	case "LIVED":
		return "survived", true
	case "TIMED OUT":
		return "timedOut", true
	case "NOT COVERED":
		return "noCoverage", true
	case "NOT VIABLE":
		return "compileError", true
	case "SKIPPED":
		return "ignored", true
	default:
		return "", false
	}
}

func supportedStrykerSchema(engine, version string) bool {
	return (engine == "stryker-js" && version == "1.0") || (engine == "stryker-net" && version == "2")
}

func normalizeReportPath(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	return strings.TrimPrefix(path.Clean(value), "./")
}

func validReportPath(value string) bool {
	if value == "" || value == "." || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "../") || value == ".." {
		return false
	}
	return !(len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':')
}

func reportProvenance(data []byte, schema *string) Provenance {
	digest := reportcontract.SHA256(data)
	return Provenance{NativeExitCode: 0, NativeReportSchemaVersion: schema, NativeReportSHA256: &digest}
}

func validPercentage(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func round(value float64) float64 { return math.Round(value*100) / 100 }
