// Package benchlat adds latency percentiles to Go benchmarks, which report only the mean.
package benchlat

import (
	"math"
	"slices"
	"strconv"
	"testing"
	"time"
)

type Recorder struct {
	samples []time.Duration
}

func (r *Recorder) Time(f func()) {
	start := time.Now()
	f()
	r.samples = append(r.samples, time.Since(start))
}

// Percentile uses the nearest-rank method, so it always returns an observed duration.
func (r *Recorder) Percentile(p float64) time.Duration {
	if len(r.samples) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(r.samples))
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	return sorted[max(rank, 1)-1]
}

func (r *Recorder) Report(b *testing.B) {
	for _, p := range []float64{50, 95, 99} {
		b.ReportMetric(float64(r.Percentile(p).Microseconds())/1000, "p"+strconv.FormatFloat(p, 'f', -1, 64)+"-ms")
	}
}
