package analysis

import "testing"

func TestParseDiffRecordsNewSideOfHunks(t *testing.T) {
	diff := `diff --git a/src/Example.cs b/src/Example.cs
--- a/src/Example.cs
+++ b/src/Example.cs
@@ -2,2 +2,3 @@
+changed
@@ -10 +11,0 @@
-deleted
`
	changes := parseDiff(diff)
	for _, line := range []int{2, 3, 4} {
		if _, ok := changes["src/Example.cs"][line]; !ok {
			t.Errorf("expected line %d to be changed", line)
		}
	}
	if _, ok := changes["src/Example.cs"][11]; ok {
		t.Error("deletion-only hunk must not mark a line in the new file")
	}
}
