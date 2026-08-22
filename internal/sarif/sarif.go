package sarif

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const schema = "https://json.schemastore.org/sarif-2.1.0.json"
const MaxResults = 25000

type Log struct {
	Version string `json:"version"`
	Schema  string `json:"$schema"`
	Runs    []Run  `json:"runs"`
}

type Run struct {
	Tool       Tool     `json:"tool"`
	ColumnKind string   `json:"columnKind"`
	Results    []Result `json:"results"`
}

type Tool struct {
	Driver Driver `json:"driver"`
}

type Driver struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Rule struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	ShortDescription Message `json:"shortDescription"`
	FullDescription  Message `json:"fullDescription"`
	Help             Message `json:"help"`
}

type Result struct {
	RuleID              string            `json:"ruleId"`
	Level               string            `json:"level"`
	Message             Message           `json:"message"`
	Locations           []Location        `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          any               `json:"properties"`
}

type Message struct {
	Text string `json:"text"`
}

type Location struct {
	PhysicalLocation PhysicalLocation `json:"physicalLocation"`
}

type PhysicalLocation struct {
	ArtifactLocation ArtifactLocation `json:"artifactLocation"`
	Region           Region           `json:"region"`
}

type ArtifactLocation struct {
	URI string `json:"uri"`
}

type Region struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

func New(toolName, toolVersion string, rules []Rule, results []Result) Log {
	return Log{
		Version: "2.1.0",
		Schema:  schema,
		Runs: []Run{{
			Tool:       Tool{Driver: Driver{Name: toolName, Version: toolVersion, Rules: rules}},
			ColumnKind: "utf16CodeUnits",
			Results:    results,
		}},
	}
}

func CheckResultLimit(count int) error {
	if count > MaxResults {
		return fmt.Errorf("SARIF result count %d exceeds GitHub limit %d", count, MaxResults)
	}
	return nil
}

func URI(value string) string {
	normalized := strings.TrimPrefix(path.Clean(strings.ReplaceAll(value, "\\", "/")), "./")
	escaped := (&url.URL{Path: normalized}).EscapedPath()
	if slash := strings.IndexByte(escaped, '/'); slash >= 0 {
		return strings.ReplaceAll(escaped[:slash], ":", "%3A") + escaped[slash:]
	}
	return strings.ReplaceAll(escaped, ":", "%3A")
}
