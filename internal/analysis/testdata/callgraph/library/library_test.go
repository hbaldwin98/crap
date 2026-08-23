package library

import "testing"

func TestFormalGreeting(t *testing.T) {
	if got := FormalGreeting("world"); got != "hello world" {
		t.Fatalf("unexpected greeting %q", got)
	}
}

func TestShout(t *testing.T) {
	if got := shout("world"); got != "HEY WORLD" {
		t.Fatalf("unexpected shout %q", got)
	}
}

func helperTotals(t *testing.T) int {
	t.Helper()
	return total([]int{1, 2, 3}) + len(doubles([]int{1}))
}
