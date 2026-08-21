package analysis

import "testing"

func TestCRAPScore(t *testing.T) {
	tests := []struct {
		name       string
		complexity int
		coverage   float64
		want       float64
	}{
		{name: "no coverage", complexity: 5, coverage: 0, want: 30},
		{name: "full coverage", complexity: 5, coverage: 100, want: 5},
		{name: "partial coverage", complexity: 10, coverage: 50, want: 22.5},
		{name: "clamps coverage", complexity: 3, coverage: 150, want: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CRAPScore(test.complexity, test.coverage); got != test.want {
				t.Fatalf("CRAPScore(%d, %.2f) = %.2f, want %.2f", test.complexity, test.coverage, got, test.want)
			}
		})
	}
}
