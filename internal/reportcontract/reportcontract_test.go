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
