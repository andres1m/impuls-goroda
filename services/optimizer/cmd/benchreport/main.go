// Command benchreport turns `go test -bench` output of the optimizer core into a Markdown report.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type result struct {
	Name                                   string
	Runs, Iters                            int
	NsOp, P50, P95, P99, BytesOp, AllocsOp float64
}

// report holds the medians of one `go test -bench` run and the GOMAXPROCS it ran with.
type report struct {
	Procs   int
	Results []result
}

const nsPerMs = 1e6

var (
	procSuffix = regexp.MustCompile(`-(\d+)$`)
	units      = []string{"ns/op", "p50-ms", "p95-ms", "p99-ms", "B/op", "allocs/op"}
)

type parser struct {
	samples map[string]map[string][]float64
	order   []string
	procs   int
	// A benchmark that logs prints its name, then the log, then the numbers on a line of their own.
	pending string
}

// parse keeps the median of repeated runs, which a single slow run cannot drag. Anything it cannot
// account for fails the report rather than quietly leaving a benchmark out.
func parse(r io.Reader) (report, error) {
	p := &parser{samples: map[string]map[string][]float64{}}
	scan := bufio.NewScanner(r)
	for scan.Scan() {
		if err := p.feed(scan.Text()); err != nil {
			return report{}, err
		}
	}
	if err := scan.Err(); err != nil {
		return report{}, fmt.Errorf("scan benchmark output: %w", err)
	}
	return p.buildReport()
}

func (p *parser) feed(line string) error {
	if strings.HasPrefix(line, "--- FAIL") || strings.HasPrefix(line, "FAIL") {
		return errors.New("benchmark run failed: " + line)
	}
	if strings.HasPrefix(line, "--- SKIP") {
		return errors.New("benchmark skipped: " + line)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	name, rest, ok, err := p.resolveName(line, fields)
	if err != nil || !ok {
		return err
	}
	name, err = p.parseProcs(name)
	if err != nil {
		return err
	}
	return p.recordSample(name, line, rest)
}

func (p *parser) resolveName(
	line string,
	fields []string,
) (name string, rest []string, ok bool, err error) {
	switch {
	case strings.HasPrefix(fields[0], "Benchmark") && len(fields) == 1:
		p.pending = fields[0]
		return "", nil, false, nil
	case strings.HasPrefix(fields[0], "Benchmark"):
		if p.pending != "" {
			return "", nil, false, fmt.Errorf("%s: no result", p.pending)
		}
		return fields[0], fields[1:], true, nil
	case len(fields) >= 3 && isNumber(fields[0]) && strings.HasSuffix(fields[2], "/op"):
		if p.pending == "" {
			return "", nil, false, errors.New("benchmark result without a name: " + line)
		}
		name = p.pending
		p.pending = ""
		return name, fields, true, nil
	default:
		return "", nil, false, nil
	}
}

func (p *parser) parseProcs(name string) (string, error) {
	// go test leaves the suffix out when GOMAXPROCS is 1.
	procs := 1
	if m := procSuffix.FindStringSubmatch(name); m != nil {
		parsed, err := strconv.Atoi(m[1])
		if err != nil {
			return "", fmt.Errorf("%s: parse GOMAXPROCS: %w", name, err)
		}
		procs = parsed
		name = strings.TrimSuffix(name, m[0])
	}
	if p.procs != 0 && procs != p.procs {
		return "", fmt.Errorf("%s: GOMAXPROCS %d, other benchmarks ran with %d", name, procs, p.procs)
	}
	p.procs = procs
	return name, nil
}

func (p *parser) recordSample(name, line string, fields []string) error {
	if len(fields) < 3 || len(fields)%2 != 1 {
		return fmt.Errorf("%s: malformed result line: %s", name, line)
	}
	if p.samples[name] == nil {
		p.samples[name] = map[string][]float64{}
		p.order = append(p.order, name)
	}
	iters, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	p.samples[name]["iters"] = append(p.samples[name]["iters"], iters)
	for i := 1; i+1 < len(fields); i += 2 {
		v, parseErr := strconv.ParseFloat(fields[i], 64)
		if parseErr != nil {
			return fmt.Errorf("%s: %w", name, parseErr)
		}
		p.samples[name][fields[i+1]] = append(p.samples[name][fields[i+1]], v)
	}
	return nil
}

func (p *parser) buildReport() (report, error) {
	if p.pending != "" {
		return report{}, fmt.Errorf("%s: no result", p.pending)
	}
	if len(p.order) == 0 {
		return report{}, errors.New("no benchmark results")
	}
	runs := len(p.samples[p.order[0]]["ns/op"])
	out := make([]result, 0, len(p.order))
	for _, name := range p.order {
		m := p.samples[name]
		for _, unit := range units {
			if len(m[unit]) != runs {
				return report{}, fmt.Errorf("%s: %d runs of %s, expected %d", name, len(m[unit]), unit, runs)
			}
		}
		out = append(out, result{
			Name: name, Runs: runs, Iters: int(median(m["iters"])), NsOp: median(m["ns/op"]),
			P50: median(m["p50-ms"]), P95: median(m["p95-ms"]), P99: median(m["p99-ms"]),
			BytesOp: median(m["B/op"]), AllocsOp: median(m["allocs/op"]),
		})
	}
	return report{Procs: p.procs, Results: out}, nil
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	return s[len(s)/2]
}

func render(w io.Writer, env map[string]string, rep report, cpuTop, memTop string) error {
	var b strings.Builder
	b.WriteString("# Optimizer core benchmarks\n\n")
	b.WriteString(
		"Generated by `make bench-optimizer`. The numbers describe one machine; each row is the median of the repeated runs. They are measurements, not guarantees.\n\n",
	)
	b.WriteString("## Environment\n\n")
	env = maps.Clone(env)
	env["GOMAXPROCS"] = strconv.Itoa(rep.Procs)
	for _, k := range slices.Sorted(maps.Keys(env)) {
		fmt.Fprintf(&b, "- %s: %s\n", k, env[k])
	}
	b.WriteString("\n## What is measured\n\n")
	b.WriteString("One in-process call with the candidate pool already in memory and straight-line travel estimates. " +
		"gRPC, protobuf mapping, PostgreSQL, the routing engine, Redis, the network and text embedding are outside the measurement. " +
		"Pools are synthetic and fixed, so runs are comparable. Each row is the median over the repeated runs. " +
		"Percentiles are taken over the iterations of one run, so with few iterations p95 and p99 both fall on the slowest one.\n\n")
	b.WriteString("## Results\n\n")
	b.WriteString("| Benchmark | Runs | iterations/run | mean ms | p50 ms | p95 ms | p99 ms | B/op | allocs/op |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rep.Results {
		fmt.Fprintf(
			&b,
			"| %s | %d | %d | %.3f | %.3f | %.3f | %.3f | %.0f | %.0f |\n",
			r.Name,
			r.Runs,
			r.Iters,
			r.NsOp/nsPerMs,
			r.P50,
			r.P95,
			r.P99,
			r.BytesOp,
			r.AllocsOp,
		)
	}
	fmt.Fprintf(&b, "\n## CPU profile, top functions\n\n```\n%s\n```\n", strings.TrimSpace(cpuTop))
	fmt.Fprintf(&b, "\n## Allocation profile, top functions\n\n```\n%s\n```\n", strings.TrimSpace(memTop))
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

type envFlags map[string]string

func (e envFlags) String() string { return "" }

func (e envFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok {
		return errors.New("env must be key=value")
	}
	e[k] = val
	return nil
}

func main() {
	env := envFlags{}
	bench := flag.String("bench", "", "go test -bench output")
	cpu := flag.String("cpu-top", "", "go tool pprof -top output for CPU")
	mem := flag.String("mem-top", "", "go tool pprof -top output for allocations")
	flag.Var(env, "env", "key=value line for the environment section, repeatable")
	flag.Parse()
	if err := run(*bench, *cpu, *mem, env, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(bench, cpu, mem string, env map[string]string, w io.Writer) error {
	f, err := os.Open(bench)
	if err != nil {
		return fmt.Errorf("open bench output: %w", err)
	}
	defer f.Close()
	rep, err := parse(f)
	if err != nil {
		return err
	}
	cpuTop, err := os.ReadFile(cpu)
	if err != nil {
		return fmt.Errorf("read cpu profile: %w", err)
	}
	memTop, err := os.ReadFile(mem)
	if err != nil {
		return fmt.Errorf("read mem profile: %w", err)
	}
	return render(w, env, rep, string(cpuTop), string(memTop))
}
