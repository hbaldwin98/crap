package reportcontract

import "testing"

func TestFingerprintPreservesFieldBoundaries(t *testing.T) {
	if Fingerprint("ab", "c") == Fingerprint("a", "bc") {
		t.Fatal("fingerprints with different field boundaries matched")
	}
	if got := SHA256([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("SHA256 = %s", got)
	}
}

func TestSortFilesOrdersByPathThenDigest(t *testing.T) {
	files := []FileFingerprint{
		{Path: "z.go", SHA256: "1"},
		{Path: "a.go", SHA256: "2"},
		{Path: "a.go", SHA256: "1"},
	}

	SortFiles(files)
	want := []FileFingerprint{
		{Path: "a.go", SHA256: "1"},
		{Path: "a.go", SHA256: "2"},
		{Path: "z.go", SHA256: "1"},
	}
	for index := range want {
		if files[index] != want[index] {
			t.Fatalf("files[%d] = %#v, want %#v", index, files[index], want[index])
		}
	}
}
