package mutation

import (
	"encoding/json"
	"testing"
)

func TestParseStrykerNormalizesAndScoresMutants(t *testing.T) {
	data := []byte(`{
		"schemaVersion":"1.0",
		"files": {
			"src/z.ts": {"mutants": [
				{"id":"2","mutatorName":"BooleanLiteral","status":"NoCoverage","location":{"start":{"line":9,"column":3}}},
				{"id":"1","mutatorName":"EqualityOperator","status":"Killed","location":{"start":{"line":2,"column":1}}}
			]},
			"src/a.ts": {"mutants": [
				{"id":"4","mutatorName":"BlockStatement","status":"Timeout","location":{"start":{"line":4,"column":2}}},
				{"id":"3","mutatorName":"ArithmeticOperator","status":"Survived","replacement":"-","location":{"start":{"line":3,"column":2}}},
				{"id":"5","mutatorName":"StringLiteral","status":"CompileError","location":{"start":{"line":5,"column":2}}}
			]}
		}
	}`)
	report, err := parseStryker(data, "typescript", "stryker-js", 50)
	if err != nil {
		t.Fatal(err)
	}
	if report.Score == nil || *report.Score != 50 || !report.Passed || report.ScoreSource != "report-statuses" {
		t.Fatalf("unexpected score: %#v", report)
	}
	if report.Summary.Total != 5 || report.Summary.Killed != 1 || report.Summary.TimedOut != 1 || report.Summary.CompileError != 1 {
		t.Fatalf("unexpected summary: %#v", report.Summary)
	}
	if report.Mutants[0].File != "src/a.ts" || report.Mutants[0].ID != "3" || report.Mutants[0].Status != "survived" {
		t.Fatalf("mutants are not deterministically sorted: %#v", report.Mutants)
	}
}

func TestParseGremlinsUsesEngineScore(t *testing.T) {
	data := []byte(`{
		"test_efficacy":66.6666,
		"files":[{"file_name":"work.go","mutations":[
			{"line":7,"column":4,"type":"CONDITIONALS_NEGATION","status":"KILLED"},
			{"line":8,"column":4,"type":"ARITHMETIC_BASE","status":"LIVED"},
			{"line":9,"column":4,"type":"INVERT_NEGATIVES","status":"NOT VIABLE"}
		]}]
	}`)
	report, err := parseGremlins(data, 70)
	if err != nil {
		t.Fatal(err)
	}
	if report.Score == nil || *report.Score != 66.67 || report.Passed || report.ScoreSource != "engine" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Summary.CompileError != 1 || report.Mutants[1].Status != "survived" {
		t.Fatalf("unexpected normalization: %#v", report)
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatal(err)
	}
}

func TestParseStrykerRejectsInvalidReport(t *testing.T) {
	if _, err := parseStryker([]byte(`{}`), "csharp", "stryker-net", 80); err == nil {
		t.Fatal("expected missing schema error")
	}
	if _, err := parseStryker([]byte(`{"schemaVersion":"1.0"}`), "csharp", "stryker-net", 80); err == nil {
		t.Fatal("expected missing files error")
	}
	if _, err := parseGremlins([]byte(`{"files":[]}`), 80); err == nil {
		t.Fatal("expected missing engine score error")
	}
}
