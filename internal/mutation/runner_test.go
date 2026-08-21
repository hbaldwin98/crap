package mutation

import (
	"strings"
	"testing"
)

func TestTailBufferRetainsBoundedOutputSuffix(t *testing.T) {
	buffer := &tailBuffer{limit: 5}
	for _, value := range []string{"abc", "def", "ghijkl"} {
		written, err := buffer.Write([]byte(value))
		if err != nil || written != len(value) {
			t.Fatalf("Write(%q) = %d, %v", value, written, err)
		}
	}
	if got := buffer.String(); got != "hijkl" {
		t.Fatalf("buffer = %q", got)
	}
	if len(buffer.String()) > buffer.limit || strings.Contains(buffer.String(), "a") {
		t.Fatalf("buffer exceeded its limit: %q", buffer.String())
	}
}
