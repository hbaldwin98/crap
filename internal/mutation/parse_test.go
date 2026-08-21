package mutation

import (
	"encoding/json"
	"testing"
)

func TestParseStrykerNormalizesAndScoresMutants(t *testing.T) {
	data := []byte(`{
		"schemaVersion":"1.0",
		"thresholds":{"high":80,"low":60,"break":null},
		"files": {
			"src/z.ts": {"language":"typescript","source":"z","mutants": [
				{"id":"2","mutatorName":"BooleanLiteral","status":"NoCoverage","location":{"start":{"line":9,"column":3},"end":{"line":9,"column":4}}},
				{"id":"1","mutatorName":"EqualityOperator","status":"Killed","location":{"start":{"line":2,"column":1},"end":{"line":2,"column":2}}}
			]},
			"src/a.ts": {"language":"typescript","source":"a","mutants": [
				{"id":"4","mutatorName":"BlockStatement","status":"Timeout","location":{"start":{"line":4,"column":2},"end":{"line":4,"column":3}}},
				{"id":"3","mutatorName":"ArithmeticOperator","status":"Survived","replacement":"-","location":{"start":{"line":3,"column":2},"end":{"line":3,"column":3}}},
				{"id":"5","mutatorName":"StringLiteral","status":"CompileError","location":{"start":{"line":5,"column":2},"end":{"line":5,"column":3}}}
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
	if report.SchemaVersion != "2" || report.Provenance.NativeReportSchemaVersion == nil || *report.Provenance.NativeReportSchemaVersion != "1.0" || report.Provenance.NativeReportSHA256 == nil || len(*report.Provenance.NativeReportSHA256) != 64 {
		t.Fatalf("unexpected provenance: %#v", report.Provenance)
	}
}

func TestParseGremlinsAllowsOnlyUncoveredMutants(t *testing.T) {
	data := []byte(`{"test_efficacy":0,"mutants_total":0,"mutants_killed":0,"mutants_lived":0,"mutants_not_viable":0,"mutants_not_covered":1,"files":[{"file_name":"work.go","mutations":[{"line":1,"column":1,"type":"x","status":"NOT COVERED"}]}]}`)
	report, err := parseGremlins(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Score == nil || *report.Score != 0 || report.Summary.NoCoverage != 1 || report.Provenance.NativeReportSHA256 == nil {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestParseGremlinsUsesEngineScore(t *testing.T) {
	data := []byte(`{
		"test_efficacy":66.6666,
		"mutants_total":4,
		"mutants_killed":2,
		"mutants_lived":1,
		"mutants_not_viable":1,
		"mutants_not_covered":0,
		"files":[{"file_name":"work.go","mutations":[
			{"line":7,"column":4,"type":"CONDITIONALS_NEGATION","status":"KILLED"},
			{"line":8,"column":4,"type":"ARITHMETIC_BASE","status":"LIVED"},
			{"line":9,"column":4,"type":"INVERT_NEGATIVES","status":"NOT VIABLE"},
			{"line":10,"column":4,"type":"CONDITIONALS_BOUNDARY","status":"KILLED"}
		]}]
	}`)
	report, err := parseGremlins(data, 70)
	if err != nil {
		t.Fatal(err)
	}
	if report.Score == nil || *report.Score != 66.67 || report.Passed || report.ScoreSource != "engine" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Summary.Killed != 2 || report.Summary.CompileError != 1 || report.Mutants[1].Status != "survived" {
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

func TestParseStrykerRejectsInvalidMutants(t *testing.T) {
	tests := map[string]string{
		"unsupported schema":         `{"schemaVersion":"3","thresholds":{"high":80,"low":60,"break":null},"files":{}}`,
		"future minor":               `{"schemaVersion":"1.1","thresholds":{"high":80,"low":60,"break":null},"files":{}}`,
		"malformed schema":           `{"schemaVersion":"1.garbage","thresholds":{"high":80,"low":60,"break":null},"files":{}}`,
		"missing file fields":        `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"mutants":[]}}}`,
		"missing mutants":            `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x"}}}`,
		"missing id":                 `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x","mutants":[{"mutatorName":"x","status":"Killed","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":2}}}]}}}`,
		"duplicate id":               `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x","mutants":[{"id":"1","mutatorName":"x","status":"Killed","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":2}}},{"id":"1","mutatorName":"x","status":"Killed","location":{"start":{"line":2,"column":1},"end":{"line":2,"column":2}}}]}}}`,
		"unknown status":             `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x","mutants":[{"id":"1","mutatorName":"x","status":"Pending","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":2}}}]}}}`,
		"non-schema status spelling": `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x","mutants":[{"id":"1","mutatorName":"x","status":"killed","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":2}}}]}}}`,
		"missing location":           `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x","mutants":[{"id":"1","mutatorName":"x","status":"Killed"}]}}}`,
		"missing end location":       `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x","mutants":[{"id":"1","mutatorName":"x","status":"Killed","location":{"start":{"line":1,"column":1}}}]}}}`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseStryker([]byte(data), "typescript", "stryker-js", 80); err == nil {
				t.Fatal("expected report validation error")
			}
		})
	}
}

func TestParseStrykerKeepsRuntimeErrorsDistinct(t *testing.T) {
	data := []byte(`{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{"work.ts":{"language":"typescript","source":"x","mutants":[{"id":"1","mutatorName":"x","status":"RuntimeError","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":2}}}]}}}`)
	report, err := parseStryker(data, "typescript", "stryker-js", 80)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.RuntimeError != 1 || report.Summary.CompileError != 0 || report.Mutants[0].Status != "runtimeError" {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestParseStrykerUsesEngineSpecificSchema(t *testing.T) {
	if _, err := parseStryker([]byte(`{"schemaVersion":"2","thresholds":{"high":80,"low":60,"break":0},"files":{}}`), "csharp", "stryker-net", 80); err != nil {
		t.Fatal(err)
	}
	if _, err := parseStryker([]byte(`{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":0},"files":{}}`), "csharp", "stryker-net", 80); err == nil {
		t.Fatal("expected Stryker.NET schema mismatch")
	}
}

func TestParseGremlinsRejectsInconsistentScoreAndCounts(t *testing.T) {
	tests := []string{
		`{"test_efficacy":100,"mutants_total":1,"mutants_killed":1,"mutants_lived":0,"mutants_not_viable":0,"mutants_not_covered":0,"files":[{"file_name":"work.go","mutations":[{"line":1,"column":1,"type":"x","status":"LIVED"}]}]}`,
		`{"test_efficacy":50,"mutants_total":1,"mutants_killed":1,"mutants_lived":0,"mutants_not_viable":0,"mutants_not_covered":0,"files":[{"file_name":"work.go","mutations":[{"line":1,"column":1,"type":"x","status":"KILLED"}]}]}`,
		`{"test_efficacy":100,"mutants_total":1,"mutants_killed":1,"mutants_lived":0,"mutants_not_viable":0,"mutants_not_covered":0,"files":[{"file_name":"work.go","mutations":[{"line":1,"column":1,"type":"x","status":"UNKNOWN"}]}]}`,
	}
	for _, data := range tests {
		if _, err := parseGremlins([]byte(data), 80); err == nil {
			t.Errorf("expected report validation error for %s", data)
		}
	}
}
