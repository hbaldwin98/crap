package mutation

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

type strykerReport struct {
	SchemaVersion string `json:"schemaVersion"`
	Files         map[string]struct {
		Mutants []struct {
			ID           string `json:"id"`
			MutatorName  string `json:"mutatorName"`
			Replacement  string `json:"replacement"`
			Description  string `json:"description"`
			Status       string `json:"status"`
			StatusReason string `json:"statusReason"`
			Location     struct {
				Start struct {
					Line   int `json:"line"`
					Column int `json:"column"`
				} `json:"start"`
			} `json:"location"`
		} `json:"mutants"`
	} `json:"files"`
}

type gremlinsReport struct {
	TestEfficacy *float64 `json:"test_efficacy"`
	Files        []struct {
		FileName  string `json:"file_name"`
		Mutations []struct {
			Line   int    `json:"line"`
			Column int    `json:"column"`
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
	mutants := make([]MutantResult, 0)
	for file, result := range raw.Files {
		for index, mutant := range result.Mutants {
			id := mutant.ID
			if id == "" {
				id = fmt.Sprintf("%s:%d:%d:%s:%d", filepath.ToSlash(file), mutant.Location.Start.Line, mutant.Location.Start.Column, mutant.MutatorName, index)
			}
			reason := mutant.StatusReason
			if reason == "" {
				reason = mutant.Description
			}
			mutants = append(mutants, MutantResult{
				ID: id, File: filepath.ToSlash(file), Line: mutant.Location.Start.Line,
				Column: mutant.Location.Start.Column, Mutator: mutant.MutatorName,
				Status: normalizeStatus(mutant.Status), Replacement: mutant.Replacement, Reason: reason,
			})
		}
	}
	return makeReport(language, engine, "report-statuses", minimum, nil, mutants), nil
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
	mutants := make([]MutantResult, 0)
	for _, file := range raw.Files {
		for index, mutant := range file.Mutations {
			mutants = append(mutants, MutantResult{
				ID:   fmt.Sprintf("%s:%d:%d:%s:%d", filepath.ToSlash(file.FileName), mutant.Line, mutant.Column, mutant.Type, index),
				File: filepath.ToSlash(file.FileName), Line: mutant.Line, Column: mutant.Column,
				Mutator: mutant.Type, Status: normalizeStatus(mutant.Status),
			})
		}
	}
	score := round(*raw.TestEfficacy)
	return makeReport("go", "gremlins", "engine", minimum, &score, mutants), nil
}

func makeReport(language, engine, source string, minimum float64, engineScore *float64, mutants []MutantResult) Report {
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
		Passed: score != nil && *score >= minimum, Summary: summary, Mutants: mutants,
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
		case "ignored":
			summary.Ignored++
		default:
			summary.Other++
		}
	}
	return summary
}

func normalizeStatus(status string) string {
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(status))
	switch compact {
	case "killed":
		return "killed"
	case "survived", "lived":
		return "survived"
	case "timeout", "timedout":
		return "timedOut"
	case "nocoverage", "notcovered":
		return "noCoverage"
	case "compileerror", "notviable":
		return "compileError"
	case "ignored", "skipped":
		return "ignored"
	default:
		if compact == "" {
			return "unknown"
		}
		return compact
	}
}

func round(value float64) float64 { return math.Round(value*100) / 100 }
