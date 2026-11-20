package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type benchmark struct {
	Workload  string              `json:"workload"`
	Scenarios map[string]scenario `json:"scenarios"`
}

type scenario struct {
	Requests            int     `json:"requests"`
	Failed              int     `json:"failed"`
	P95TTFTMilliseconds float64 `json:"p95_ttft_ms"`
	CompletedPerSecond  float64 `json:"completed_per_second"`
}

type policy struct {
	MinRequests          int     `json:"min_requests"`
	MaxTTFTGrowthPct     float64 `json:"max_p95_ttft_growth_pct"`
	MaxThroughputDropPct float64 `json:"max_throughput_drop_pct"`
	MaxCandidateErrorPct float64 `json:"max_candidate_error_pct"`
}

func readJSON(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: unexpected trailing data", path)
	}
	return nil
}

func valid(s scenario, min int) bool {
	return s.Requests >= min && s.Failed >= 0 && s.Failed <= s.Requests &&
		s.P95TTFTMilliseconds >= 0 && s.CompletedPerSecond >= 0 &&
		(s.Failed == s.Requests && s.P95TTFTMilliseconds == 0 && s.CompletedPerSecond == 0 ||
			s.Failed < s.Requests && s.P95TTFTMilliseconds > 0 && s.CompletedPerSecond > 0)
}

func run(args []string, out, stderr io.Writer) int {
	fs := flag.NewFlagSet("tokengate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	basePath := fs.String("baseline", "", "known-good benchmark JSON")
	candidatePath := fs.String("candidate", "", "candidate benchmark JSON")
	policyPath := fs.String("policy", "", "thresholds JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	bad := func(message string) int { fmt.Fprintln(stderr, "INVALID:", message); return 2 }
	if *basePath == "" || *candidatePath == "" || *policyPath == "" || fs.NArg() != 0 {
		return bad("provide -baseline, -candidate, and -policy")
	}
	var b, c benchmark
	p := policy{MinRequests: -1, MaxTTFTGrowthPct: -1, MaxThroughputDropPct: -1, MaxCandidateErrorPct: -1}
	for _, input := range []struct {
		path string
		dst  any
	}{{*basePath, &b}, {*candidatePath, &c}, {*policyPath, &p}} {
		if err := readJSON(input.path, input.dst); err != nil {
			return bad(err.Error())
		}
	}
	if p.MinRequests < 1 || p.MaxTTFTGrowthPct < 0 || p.MaxThroughputDropPct < 0 || p.MaxThroughputDropPct > 100 || p.MaxCandidateErrorPct < 0 || p.MaxCandidateErrorPct >= 100 {
		return bad("policy requires minimum requests >= 1, nonnegative TTFT growth, throughput drop in [0,100] and error rate in [0,100)")
	}
	if strings.TrimSpace(b.Workload) == "" || b.Workload != c.Workload {
		return bad("workload IDs must match")
	}
	if len(b.Scenarios) == 0 || len(b.Scenarios) != len(c.Scenarios) {
		return bad("baseline and candidate require the same nonempty scenario set")
	}
	names := make([]string, 0, len(b.Scenarios))
	for name, baseline := range b.Scenarios {
		candidate, ok := c.Scenarios[name]
		if !ok || strings.TrimSpace(name) == "" {
			return bad("baseline and candidate require identical named scenarios")
		}
		if !valid(baseline, p.MinRequests) || !valid(candidate, p.MinRequests) || baseline.Failed == baseline.Requests ||
			100*float64(baseline.Failed)/float64(baseline.Requests) > p.MaxCandidateErrorPct {
			return bad(fmt.Sprintf("invalid or insufficient evidence for %q, or baseline exceeds the error budget", name))
		}
		names = append(names, name)
	}
	sort.Strings(names)
	blocked := false
	for _, name := range names {
		baseline, candidate := b.Scenarios[name], c.Scenarios[name]
		growth := 100 * (candidate.P95TTFTMilliseconds - baseline.P95TTFTMilliseconds) / baseline.P95TTFTMilliseconds
		drop := 100 * (baseline.CompletedPerSecond - candidate.CompletedPerSecond) / baseline.CompletedPerSecond
		errorPct := 100 * float64(candidate.Failed) / float64(candidate.Requests)
		fmt.Fprintf(out, "%s: p95 TTFT %.1f → %.1f ms; throughput %.2f → %.2f req/s; candidate errors %d/%d (%.2f%%)\n",
			name, baseline.P95TTFTMilliseconds, candidate.P95TTFTMilliseconds, baseline.CompletedPerSecond, candidate.CompletedPerSecond, candidate.Failed, candidate.Requests, errorPct)
		if candidate.Failed == candidate.Requests {
			fmt.Fprintln(out, "  BLOCK: no successful candidate requests")
			blocked = true
		} else if growth > p.MaxTTFTGrowthPct {
			fmt.Fprintf(out, "  BLOCK: p95 TTFT grew %.2f%% (budget %.2f%%)\n", growth, p.MaxTTFTGrowthPct)
			blocked = true
		}
		if drop > p.MaxThroughputDropPct {
			fmt.Fprintf(out, "  BLOCK: throughput fell %.2f%% (budget %.2f%%)\n", drop, p.MaxThroughputDropPct)
			blocked = true
		}
		if errorPct > p.MaxCandidateErrorPct {
			fmt.Fprintf(out, "  BLOCK: error rate %.2f%% (budget %.2f%%)\n", errorPct, p.MaxCandidateErrorPct)
			blocked = true
		}
	}
	if blocked {
		fmt.Fprintln(out, "BLOCK: candidate failed release policy")
		return 1
	}
	fmt.Fprintln(out, "PASS: candidate met release policy")
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
