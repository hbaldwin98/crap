package buildinfo

import "testing"

func TestToolUsesLinkerMetadata(t *testing.T) {
	originalVersion, originalRevision, originalModified := Version, Revision, Modified
	t.Cleanup(func() { Version, Revision, Modified = originalVersion, originalRevision, originalModified })
	Version = "9.8.7"
	Revision = "0123456789abcdef"
	Modified = "true"

	tool := Tool("crap-test")
	if tool.Name != "crap-test" || tool.Version != Version || tool.Revision != Revision || !tool.Modified {
		t.Fatalf("tool identity = %#v", tool)
	}
}

func TestToolRejectsInvalidModifiedLinkerValue(t *testing.T) {
	originalVersion, originalRevision, originalModified := Version, Revision, Modified
	t.Cleanup(func() { Version, Revision, Modified = originalVersion, originalRevision, originalModified })
	Version = "9.8.7"
	Revision = "explicit"
	Modified = "invalid"

	if tool := Tool("crap"); tool.Modified {
		t.Fatalf("invalid modified value produced %#v", tool)
	}
}
