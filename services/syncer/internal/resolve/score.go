package resolve

import "math"

const (
	Radius    = 50.0
	Threshold = 0.85

	weightTrigram  = 0.45
	weightVector   = 0.40
	weightDistance = 0.15
)

func proximity(distance float64) float64 {
	return math.Max(0, 1-distance/Radius)
}

func Score(trigram, cosine, distance float64) float64 {
	return weightTrigram*trigram + weightVector*cosine + weightDistance*proximity(distance)
}

// Reachable tells whether the pair could pass the threshold if the vectors were identical, so a vector
// is worth fetching only for such pairs.
func Reachable(trigram, distance float64) bool {
	return weightTrigram*trigram+weightDistance*proximity(distance)+weightVector >= Threshold
}

func Cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
