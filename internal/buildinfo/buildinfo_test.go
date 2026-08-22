package buildinfo

import (
	"runtime/debug"
	"testing"
)

func preserveBuildInfo(t *testing.T) {
	t.Helper()
	originalVersion, originalRevision, originalModified, originalRead := Version, Revision, Modified, readBuildInfo
	t.Cleanup(func() {
		Version, Revision, Modified, readBuildInfo = originalVersion, originalRevision, originalModified, originalRead
	})
}

func TestToolUsesLinkerMetadata(t *testing.T) {
	preserveBuildInfo(t)
	Version = "9.8.7"
	Revision = "0123456789abcdef"
	Modified = "true"

	tool := Tool("crap-test")
	if tool.Name != "crap-test" || tool.Version != Version || tool.Revision != Revision || !tool.Modified {
		t.Fatalf("tool identity = %#v", tool)
	}
}

func TestToolRejectsInvalidModifiedLinkerValue(t *testing.T) {
	preserveBuildInfo(t)
	Version = "9.8.7"
	Revision = "explicit"
	Modified = "invalid"

	if tool := Tool("crap"); tool.Modified {
		t.Fatalf("invalid modified value produced %#v", tool)
	}
}

func TestToolUsesVCSMetadataWhenLinkerMetadataIsAbsent(t *testing.T) {
	preserveBuildInfo(t)
	Version, Revision, Modified = "0.2.0", "", ""
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef"},
			{Key: "vcs.modified", Value: "true"},
		}}, true
	}

	tool := Tool("crap")
	if tool.Revision != "abcdef" || !tool.Modified {
		t.Fatalf("tool identity = %#v", tool)
	}
}

func TestCurrentVersion(t *testing.T) {
	tests := []struct {
		name          string
		version       string
		moduleVersion string
		buildInfoOK   bool
		want          string
	}{
		{name: "released module", version: "0.2.0", moduleVersion: "v0.2.1", buildInfoOK: true, want: "v0.2.1"},
		{name: "linker override", version: "9.8.7", moduleVersion: "v0.2.1", buildInfoOK: true, want: "9.8.7"},
		{name: "missing module version", version: "0.2.0", buildInfoOK: true, want: "0.2.0"},
		{name: "development module", version: "0.2.0", moduleVersion: "(devel)", buildInfoOK: true, want: "0.2.0"},
		{name: "missing build info", version: "0.2.0", moduleVersion: "v0.2.1", want: "0.2.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preserveBuildInfo(t)
			Version = test.version
			readBuildInfo = func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: test.moduleVersion}}, test.buildInfoOK
			}
			if got := CurrentVersion(); got != test.want {
				t.Fatalf("CurrentVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
