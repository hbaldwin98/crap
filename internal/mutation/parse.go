package mutation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
)

type strykerReport struct {
	SchemaVersion string `json:"schemaVersion"`
	Thresholds    *struct {
		High  *float64 `json:"high"`
		Low   *float64 `json:"low"`
		Break *float64 `json:"break"`
	} `json:"thresholds"`
	Files map[string]struct {
		Language *string `json:"language"`
		Source   *string `json:"source"`
		Mutants  *[]struct {
			ID           string `json:"id"`
			MutatorName  string `json:"mutatorName"`
			Replacement  string `json:"replacement"`
			Description  string `json:"description"`
			Status       string `json:"status"`
			StatusReason string `json:"statusReason"`
			Location     *struct {
				Start *strykerPosition `json:"start"`
				End   *strykerPosition `json:"end"`
			} `json:"location"`
		} `json:"mutants"`
	} `json:"files"`
}

type gremlinsReport struct {
	TestEfficacy      *float64 `json:"test_efficacy"`
	MutantsTotal      *int     `json:"mutants_total"`
	MutantsKilled     *int     `json:"mutants_killed"`
	MutantsLived      *int     `json:"mutants_lived"`
	MutantsNotViable  *int     `json:"mutants_not_viable"`
	MutantsNotCovered *int     `json:"mutants_not_covered"`
	Files             []struct {
		FileName  string `json:"file_name"`
		Mutations *[]struct {
			Line   *int   `json:"line"`
			Column *int   `json:"column"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"mutations"`
	} `json:"files"`
}

func parseStryker(data []byte, language, engine string, minimum float64) (Report, error) {
	var raw strykerReport
	if err := json.Unmarshal(data, &raw); err != nil {
		return Report{}, fmt.Errorf("parse %s JSON report: %w", engine, err)
	}
	if raw.SchemaVersion == "" {
		return Report{}, fmt.Errorf("parse %s JSON report: missing schemaVersion", engine)
	}
	if raw.Files == nil {
		return Report{}, fmt.Errorf("parse %s JSON report: missing files", engine)
	}
	if !supportedStrykerSchema(engine, raw.SchemaVersion) {
		return Report{}, fmt.Errorf("parse %s JSON report: unsupported schemaVersion %q", engine, raw.SchemaVersion)
	}
	if raw.Thresholds == nil || raw.Thresholds.High == nil || raw.Thresholds.Low == nil {
		return Report{}, fmt.Errorf("parse %s JSON report: missing thresholds", engine)
	}
	if !validPercentage(*raw.Thresholds.High) || !validPercentage(*raw.Thresholds.Low) || *raw.Thresholds.High < *raw.Thresholds.Low || (raw.Thresholds.Break != nil && (!validPercentage(*raw.Thresholds.Break) || *raw.Thresholds.Break > *raw.Thresholds.Low)) {
		return Report{}, fmt.Errorf("parse %s JSON report: invalid thresholds", engine)
	}
	mutants := make([]MutantResult, 0)
	ids := make(map[string]struct{})
	for file, result := range raw.Files {
		file = normalizeReportPath(file)
		if file == "." || file == "" {
			return Report{}, fmt.Errorf("parse %s JSON report: empty file path", engine)
		}
		if result.Language == nil || *result.Language == "" || result.Source == nil {
			return Report{}, fmt.Errorf("parse %s JSON report: incomplete file entry for %s", engine, file)
		}
		if result.Mutants == nil {
			return Report{}, fmt.Errorf("parse %s JSON report: missing mutants for %s", engine, file)
		}
		for _, mutant := range *result.Mutants {
			if mutant.ID == "" {
				return Report{}, fmt.Errorf("parse %s JSON report: missing mutant id in %s", engine, file)
			}
			if _, duplicate := ids[mutant.ID]; duplicate {
				return Report{}, fmt.Errorf("parse %s JSON report: duplicate mutant id %q", engine, mutant.ID)
			}
			ids[mutant.ID] = struct{}{}
			if mutant.MutatorName == "" {
				return Report{}, fmt.Errorf("parse %s JSON report: mutant %s has no mutator name", engine, mutant.ID)
			}
			status, ok := strykerStatus(mutant.Status)
			if !ok {
				return Report{}, fmt.Errorf("parse %s JSON report: mutant %s has unsupported status %q", engine, mutant.ID, mutant.Status)
			}
			if mutant.Location == nil || !completeStrykerPosition(mutant.Location.Start) || !completeStrykerPosition(mutant.Location.End) {
				return Report{}, fmt.Errorf("parse %s JSON report: mutant %s has incomplete location", engine, mutant.ID)
			}
			line, column := *mutant.Location.Start.Line, *mutant.Location.Start.Column
			endLine, endColumn := *mutant.Location.End.Line, *mutant.Location.End.Column
			if line < 1 || column < 1 || endLine < line || (endLine == line && endColumn < column) {
				return Report{}, fmt.Errorf("parse %s JSON report: mutant %s has invalid location", engine, mutant.ID)
			}
			reason := mutant.StatusReason
			if reason == "" {
				reason = mutant.Description
			}
			mutants = append(mutants, MutantResult{
				ID: mutant.ID, File: file, Line: line,
				Column: column, Mutator: mutant.MutatorName,
				Status: status, Replacement: mutant.Replacement, Reason: reason,
			})
		}
	}
	provenance := reportProvenance(data, &raw.SchemaVersion)
	provenance.NativeBreakThreshold = raw.Thresholds.Break
	return makeReport(language, engine, "report-statuses", minimum, nil, mutants, provenance), nil
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
	if raw.TestEfficacy == nil {
		return Report{}, fmt.Errorf("parse Gremlins JSON report: missing test_efficacy")
	}
	if math.IsNaN(*raw.TestEfficacy) || math.IsInf(*raw.TestEfficacy, 0) || *raw.TestEfficacy < 0 || *raw.TestEfficacy > 100 {
		return Report{}, fmt.Errorf("parse Gremlins JSON report: test_efficacy must be between 0 and 100")
	}
	if raw.Files == nil {
		return Report{}, fmt.Errorf("parse Gremlins JSON report: missing files")
	}
	if raw.MutantsTotal == nil || raw.MutantsKilled == nil || raw.MutantsLived == nil || raw.MutantsNotViable == nil || raw.MutantsNotCovered == nil {
		return Report{}, fmt.Errorf("parse Gremlins JSON report: missing mutant counts")
	}
	mutants := make([]MutantResult, 0)
	ids := make(map[string]struct{})
	for _, file := range raw.Files {
		filename := normalizeReportPath(file.FileName)
		if filename == "." || filename == "" {
			return Report{}, fmt.Errorf("parse Gremlins JSON report: empty file_name")
		}
		if file.Mutations == nil {
			return Report{}, fmt.Errorf("parse Gremlins JSON report: missing mutations for %s", filename)
		}
		for _, mutant := range *file.Mutations {
			if mutant.Line == nil || mutant.Column == nil || *mutant.Line < 1 || *mutant.Column < 1 {
				return Report{}, fmt.Errorf("parse Gremlins JSON report: invalid mutation location in %s", filename)
			}
			if mutant.Type == "" {
				return Report{}, fmt.Errorf("parse Gremlins JSON report: missing mutation type in %s", filename)
			}
			status, ok := gremlinsStatus(mutant.Status)
			if !ok {
				return Report{}, fmt.Errorf("parse Gremlins JSON report: unsupported status %q", mutant.Status)
			}
			id := fmt.Sprintf("%s:%d:%d:%s", filename, *mutant.Line, *mutant.Column, mutant.Type)
			if _, duplicate := ids[id]; duplicate {
				return Report{}, fmt.Errorf("parse Gremlins JSON report: duplicate mutant %q", id)
			}
			ids[id] = struct{}{}
			mutants = append(mutants, MutantResult{
				ID: id, File: filename, Line: *mutant.Line, Column: *mutant.Column,
				Mutator: mutant.Type, Status: status,
			})
		}
	}
	summary := summarize(mutants)
	if *raw.MutantsTotal != summary.Killed+summary.Survived+summary.CompileError || *raw.MutantsKilled != summary.Killed || *raw.MutantsLived != summary.Survived || *raw.MutantsNotViable != summary.CompileError || *raw.MutantsNotCovered != summary.NoCoverage {
		return Report{}, fmt.Errorf("parse Gremlins JSON report: mutant counts do not match files")
	}
	expected := 0.0
	if summary.Killed+summary.Survived > 0 {
		expected = round(float64(summary.Killed) / float64(summary.Killed+summary.Survived) * 100)
	}
	score := round(*raw.TestEfficacy)
	if score != expected {
		return Report{}, fmt.Errorf("parse Gremlins JSON report: test_efficacy %.2f does not match mutant counts %.2f", score, expected)
	}
	return makeReport("go", "gremlins", "engine", minimum, &score, mutants, reportProvenance(data, nil)), nil
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
		SchemaVersion: SchemaVersion, Language: language, Engine: engine,
		Score: score, ScoreSource: source, MinimumScore: minimum,
		Passed: score != nil && *score >= minimum, Summary: summary, Mutants: mutants, Provenance: provenance,
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

func reportProvenance(data []byte, schema *string) Provenance {
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	return Provenance{NativeExitCode: 0, NativeReportSchemaVersion: schema, NativeReportSHA256: &digest}
}

func validPercentage(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func round(value float64) float64 { return math.Round(value*100) / 100 }
