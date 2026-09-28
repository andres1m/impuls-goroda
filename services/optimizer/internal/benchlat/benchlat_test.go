package benchlat

import (
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	var r Recorder
	for i := 1; i <= 100; i++ {
		r.samples = append(r.samples, time.Duration(i)*time.Millisecond)
	}
	for p, want := range map[float64]time.Duration{50: 50 * time.Millisecond, 95: 95 * time.Millisecond, 99: 99 * time.Millisecond, 100: 100 * time.Millisecond} {
		if got := r.Percentile(p); got != want {
			t.Errorf("p%v = %v, want %v", p, got, want)
		}
	}
}

func TestPercentileWithoutSamples(t *testing.T) {
	var r Recorder
	if got := r.Percentile(50); got != 0 {
		t.Fatalf("p50 of nothing = %v", got)
	}
}

func TestTimeRecordsEachCall(t *testing.T) {
	var r Recorder
	for range 3 {
		r.Time(func() { time.Sleep(time.Millisecond) })
	}
	if len(r.samples) != 3 || r.Percentile(50) < time.Millisecond {
		t.Fatalf("samples %v", r.samples)
	}
}
