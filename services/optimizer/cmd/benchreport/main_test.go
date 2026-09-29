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
	if len(got.Results) != 1 {
		t.Fatalf("results %+v", got.Results)
	}
	r := got.Results[0]
	if r.Name != "BenchmarkOptimize/pool=60" || r.Runs != 3 || r.Iters != 100 || r.NsOp != 3e6 || r.P50 != 2 ||
		r.P99 != 5 ||
		r.AllocsOp != 800 {
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

func TestParseRejectsSkippedBenchmark(t *testing.T) {
	in := sample + "--- SKIP: BenchmarkRepair\n"
	if _, err := parse(strings.NewReader(in)); err == nil {
		t.Fatal("skipped benchmark accepted")
	}
}

func TestParseJoinsNameSplitByLog(t *testing.T) {
	in := "BenchmarkX-16\n    bench_test.go:10: warming up\n     100\t  2000 ns/op\t 1 p50-ms\t 2 p95-ms\t 3 p99-ms\t 5 B/op\t 1 allocs/op\n"
	got, err := parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 1 || got.Results[0].Name != "BenchmarkX" || got.Results[0].NsOp != 2000 {
		t.Fatalf("results %+v", got.Results)
	}
}

func TestParseRejectsOrphanResult(t *testing.T) {
	in := "     100\t  2000 ns/op\t 1 p50-ms\t 2 p95-ms\t 3 p99-ms\t 5 B/op\t 1 allocs/op\n"
	if _, err := parse(strings.NewReader(sample + in)); err == nil {
		t.Fatal("result without a benchmark name accepted")
	}
}

func TestParseRejectsUnevenRuns(t *testing.T) {
	other := "BenchmarkRepair-16  10  100 ns/op  1 p50-ms  2 p95-ms  3 p99-ms  5 B/op  1 allocs/op\n"
	if _, err := parse(strings.NewReader(sample + other + other)); err == nil {
		t.Fatal("benchmarks with different run counts accepted")
	}
}

func TestParseReadsGOMAXPROCS(t *testing.T) {
	got, err := parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if got.Procs != 16 {
		t.Fatalf("procs %d", got.Procs)
	}
	mixed := strings.Replace(sample, "pool=60-16 ", "pool=60-8 ", 1)
	if _, err := parse(strings.NewReader(mixed)); err == nil {
		t.Fatal("runs with different GOMAXPROCS accepted")
	}
}

func TestParseTreatsMissingSuffixAsOneProc(t *testing.T) {
	single := strings.ReplaceAll(sample, "pool=60-16 ", "pool=60 ")
	got, err := parse(strings.NewReader(single))
	if err != nil {
		t.Fatal(err)
	}
	if got.Procs != 1 || got.Results[0].Name != "BenchmarkOptimize/pool=60" {
		t.Fatalf("report %+v", got)
	}
	mixed := strings.Replace(sample, "pool=60-16 ", "pool=60 ", 1)
	if _, err := parse(strings.NewReader(mixed)); err == nil {
		t.Fatal("runs with and without GOMAXPROCS suffix accepted")
	}
}

func TestRenderStatesMedianAndProcs(t *testing.T) {
	var buf bytes.Buffer
	rep, err := parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if err := render(&buf, map[string]string{}, rep, "cpu", "mem"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "- GOMAXPROCS: 16") ||
		strings.Contains(buf.String(), "one machine and one run") {
		t.Fatalf("report:\n%s", buf.String())
	}
}

func TestParseRejectsNameFollowedByAnotherBenchmark(t *testing.T) {
	in := "BenchmarkX-16\nBenchmarkY-16  10  100 ns/op  1 p50-ms  2 p95-ms  3 p99-ms  5 B/op  1 allocs/op\n"
	if _, err := parse(strings.NewReader(in)); err == nil {
		t.Fatal("a benchmark without a result accepted")
	}
}
