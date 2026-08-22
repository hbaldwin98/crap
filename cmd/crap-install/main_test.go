package main

import (
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	var stdout, stderr strings.Builder
	options, ok := parseOptions([]string{"--client", "claude,opencode", "--client=generic", "--version", "v1.2.3", "--dry-run"}, &stdout, &stderr)
	if !ok || options.version != "v1.2.3" || len(options.clients) != 2 || !options.dryRun || stderr.Len() != 0 {
		t.Fatalf("options=%+v ok=%v stderr=%q", options, ok, stderr.String())
	}
}

func TestHelp(t *testing.T) {
	var stdout, stderr strings.Builder
	options, ok := parseOptions([]string{"--help"}, &stdout, &stderr)
	if !ok || !options.help || !strings.Contains(stdout.String(), "Usage: crap-install") || stderr.Len() != 0 {
		t.Fatalf("options=%+v ok=%v stdout=%q stderr=%q", options, ok, stdout.String(), stderr.String())
	}
}

func TestParseErrors(t *testing.T) {
	tests := [][]string{
		{"--unknown"},
		{"unexpected"},
		{"--version", "main; rm -rf /"},
		{"--version", "1.2.3"},
		{"--version", "v1.2"},
		{"--client", "cursor"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr strings.Builder
			if _, ok := parseOptions(args, &stdout, &stderr); ok || stderr.Len() == 0 {
				t.Fatalf("args=%v accepted; stderr=%q", args, stderr.String())
			}
		})
	}
}

func TestValidVersion(t *testing.T) {
	for _, version := range []string{"latest", "v0.2.0", "v1.2.3-rc.1", "abcdef0", strings.Repeat("a", 40)} {
		if !validVersion(version) {
			t.Errorf("valid version %q rejected", version)
		}
	}
	for _, version := range []string{"", "main", "v1.2.3@evil", "v1.2.3junk", "v01.2.3", "ABCDEF0", strings.Repeat("a", 41)} {
		if validVersion(version) {
			t.Errorf("invalid version %q accepted", version)
		}
	}
}
