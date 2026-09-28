package main

import (
	"bytes"
	"strings"
	"testing"
)

const sample = `goos: linux
BenchmarkOptimize/pool=60-16    	     100	  2000000 ns/op	         1.500 p50-ms	         3.000 p95-ms	         4.000 p99-ms	  50000 B/op	     700 allocs/op
BenchmarkOptimize/pool=60-16    	     100	  4000000 ns/op	         2.500 p50-ms	         5.000 p95-ms	         6.000 p99-ms	  70000 B/op	     900 allocs/op
BenchmarkOptimize/pool=60-16    	     100	  3000000 ns/op	         2.000 p50-ms	         4.000 p95-ms	         5.000 p99-ms	  60000 B/op	     800 allocs/op
PASS
`

func TestParseTakesMedianOfRuns(t *testing.T) {
	got, err := parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("results %+v", got)
	}
	r := got[0]
	if r.Name != "BenchmarkOptimize/pool=60" || r.Runs != 3 || r.Iters != 100 || r.NsOp != 3e6 || r.P50 != 2 || r.P99 != 5 || r.AllocsOp != 800 {
		t.Fatalf("result %+v", r)
	}
}

func TestParseRejectsMissingPercentiles(t *testing.T) {
	if _, err := parse(strings.NewReader("BenchmarkX-16  10  100 ns/op  5 B/op  1 allocs/op\n")); err == nil {
		t.Fatal("line without percentiles accepted")
	}
}

func TestParseRejectsFailedRun(t *testing.T) {
	if _, err := parse(strings.NewReader("--- FAIL: BenchmarkX\nFAIL\n")); err == nil {
		t.Fatal("failed run accepted")
	}
}

func TestParseRejectsEmptyOutput(t *testing.T) {
	if _, err := parse(strings.NewReader("PASS\n")); err == nil {
		t.Fatal("output without benchmarks accepted")
	}
}

func TestRenderTable(t *testing.T) {
	var buf bytes.Buffer
	results, err := parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if err := render(&buf, map[string]string{"CPU": "test cpu"}, results, "cpu top", "mem top"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| BenchmarkOptimize/pool=60 | 3 | 100 | 3.000 | 2.000 | 4.000 | 5.000 | 60000 | 800 |", "- CPU: test cpu", "cpu top", "mem top"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("report misses %q:\n%s", want, buf.String())
		}
	}
}
