package sarif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestURIUsesSlashPathsAndEscapesURICharacters(t *testing.T) {
	if got := URI(`./source files\work#1.go`); got != "source%20files/work%231.go" {
		t.Fatalf("URI = %q", got)
	}
}

func TestURIEscapesColonInFirstSegment(t *testing.T) {
	if got := URI("rule:name/file.go"); got != "rule%3Aname/file.go" {
		t.Fatalf("URI = %q", got)
	}
	if got := URI("directory/rule:name.go"); got != "directory/rule:name.go" {
		t.Fatalf("URI = %q", got)
	}
}

func TestSourceConvertsUTF8ByteAndNativeUTF16Coordinates(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte("aé😀z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := ReadSource(root, "work.go")
	if err != nil {
		t.Fatal(err)
	}
	byteRegion, err := source.ByteRegion(1, 4, 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	if byteRegion.StartColumn != 3 || byteRegion.EndColumn != 5 {
		t.Fatalf("byte region = %#v", byteRegion)
	}
	nativeRegion, err := source.UTF16Region(1, 3, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if nativeRegion != byteRegion {
		t.Fatalf("native region = %#v, byte region = %#v", nativeRegion, byteRegion)
	}
	point, err := source.BytePointRegion(1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if point.StartColumn != 3 || point.EndColumn != 5 || point.StartLine != point.EndLine {
		t.Fatalf("point region = %#v", point)
	}
	endPoint, err := source.UTF16PointRegion(1, 6)
	if err != nil {
		t.Fatal(err)
	}
	if endPoint.StartColumn != 5 || endPoint.EndColumn != 6 {
		t.Fatalf("end point region = %#v", endPoint)
	}
	if _, err := source.ByteRegion(1, 5, 1, 8); err == nil {
		t.Fatal("UTF-8 split byte column was accepted")
	}
	if _, err := source.UTF16Region(1, 4, 1, 5); err == nil {
		t.Fatal("UTF-16 surrogate split was accepted")
	}
}

func TestReadSourceRejectsOutsideMissingAndSymlinkPaths(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside.go", "missing.go"} {
		if _, err := ReadSource(root, path); err == nil {
			t.Errorf("ReadSource(%q) succeeded", path)
		}
	}
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadSource(root, "link.go"); err == nil {
		t.Fatal("symlink source was accepted")
	}
}

func TestCheckResultLimitRejectsGitHubOverflow(t *testing.T) {
	if err := CheckResultLimit(MaxResults); err != nil {
		t.Fatal(err)
	}
	if err := CheckResultLimit(MaxResults + 1); err == nil {
		t.Fatal("overflow result count was accepted")
	}
}

func TestNewSerializesRequiredGitHubProfileFields(t *testing.T) {
	document := New("tool", "1.0", []Rule{{
		ID: "RULE", Name: "Rule", ShortDescription: Message{Text: "short"},
		FullDescription: Message{Text: "full"}, Help: Message{Text: "help"},
	}}, []Result{{
		RuleID: "RULE", Level: "warning", Message: Message{Text: "finding"},
		Locations: []Location{{PhysicalLocation: PhysicalLocation{
			ArtifactLocation: ArtifactLocation{URI: "work.go"},
			Region:           Region{StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2},
		}}},
		PartialFingerprints: map[string]string{"primaryLocationLineHash": "hash"}, Properties: struct{}{},
	}})
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	run := raw["runs"].([]any)[0].(map[string]any)
	if run["columnKind"] != "utf16CodeUnits" {
		t.Fatalf("columnKind = %#v", run["columnKind"])
	}
	rule := run["tool"].(map[string]any)["driver"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if rule["fullDescription"].(map[string]any)["text"] == "" || rule["help"].(map[string]any)["text"] == "" {
		t.Fatalf("rule = %#v", rule)
	}
	result := run["results"].([]any)[0].(map[string]any)
	region := result["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)["region"].(map[string]any)
	for _, field := range []string{"startLine", "startColumn", "endLine", "endColumn"} {
		if _, ok := region[field]; !ok {
			t.Fatalf("region lacks %s: %#v", field, region)
		}
	}
}
