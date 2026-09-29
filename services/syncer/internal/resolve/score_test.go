package resolve

import (
	"math"
	"testing"
)

func TestScoreFollowsTheFormula(t *testing.T) {
	// 0.45·1 + 0.40·1 + 0.15·(1 − 0/50)
	if got := Score(1, 1, 0); math.Abs(got-1) > 1e-9 {
		t.Fatalf("perfect pair scores %v", got)
	}
	// 0.45·0.5 + 0.40·0.5 + 0.15·(1 − 25/50)
	if got := Score(0.5, 0.5, 25); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("half pair scores %v", got)
	}
	// Beyond the radius the distance term is zero, not negative.
	if got := Score(1, 1, 80); math.Abs(got-0.85) > 1e-9 {
		t.Fatalf("distance beyond the radius adds nothing: %v", got)
	}
}

func TestReachableIsWhatTheVectorCannotDecide(t *testing.T) {
	// Without the vector the pair holds at most 0.45·trigram + 0.15·proximity; the vector adds at most 0.40.
	if !Reachable(1, 0) {
		t.Fatal("identical name at one spot must be checked")
	}
	if Reachable(0, 0) || Reachable(0.2, 10) {
		t.Fatal("unrelated names cannot reach the threshold even with a perfect vector")
	}
	// 0.45·0.8 + 0.15·(1 − 10/50) = 0.48 ≥ 0.45
	if !Reachable(0.8, 10) {
		t.Fatal("a close, similar pair must be checked")
	}
	// 0.45·0.7 + 0.15·(1 − 20/50) = 0.405 < 0.45
	if Reachable(0.7, 20) {
		t.Fatal("a pair whose name and distance leave the score below 0.45 cannot pass")
	}
}

func TestCosine(t *testing.T) {
	if got := Cosine([]float32{1, 0}, []float32{1, 0}); math.Abs(got-1) > 1e-9 {
		t.Fatal(got)
	}
	if got := Cosine([]float32{1, 0}, []float32{0, 1}); math.Abs(got) > 1e-9 {
		t.Fatal(got)
	}
	if Cosine([]float32{0, 0}, []float32{1, 1}) != 0 || Cosine([]float32{1}, []float32{1, 1}) != 0 {
		t.Fatal("undefined cosine is zero")
	}
}
