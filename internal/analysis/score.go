package analysis

import "math"

func CRAPScore(complexity int, coveragePercent float64) float64 {
	coverage := math.Max(0, math.Min(100, coveragePercent)) / 100
	score := float64(complexity*complexity)*math.Pow(1-coverage, 3) + float64(complexity)
	return round(score, 2)
}

func round(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}
