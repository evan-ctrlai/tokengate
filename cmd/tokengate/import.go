package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// Unknown upstream fields are allowed; required contract fields are explicit.
// This is the flat --save-result object, not PyTorch output or appended JSONL.
type vllmResult struct {
	Backend           string   `json:"backend"`
	Model             string   `json:"model_id"`
	Tokenizer         string   `json:"tokenizer_id"`
	Prompts           *int     `json:"num_prompts"`
	Completed         *int     `json:"completed"`
	Duration          *float64 `json:"duration"`
	Throughput        *float64 `json:"request_throughput"`
	TTFT              *float64 `json:"p95_ttft_ms"`
	Concurrency       *int     `json:"max_concurrency"`
	Rate              any      `json:"request_rate"`
	Burstiness        *float64 `json:"burstiness"`
	Ramp              any      `json:"ramp_up_strategy"`
	Version           string   `json:"tokengate_vllm_version"`
	Kind              string   `json:"tokengate_kind"`
	Workload          string   `json:"tokengate_workload"`
	Mix               string   `json:"tokengate_request_mix_sha256"`
	Runtime           string   `json:"tokengate_runtime"`
	Hardware          string   `json:"tokengate_hardware"`
	ModelRevision     string   `json:"tokengate_model_revision"`
	TokenizerRevision string   `json:"tokengate_tokenizer_revision"`
	Warmup            string   `json:"tokengate_warmup"`
	Repeat            string   `json:"tokengate_repeat"`
}

// This deliberately supports one documented workload family, not arbitrary
// benchmark traffic. The hashed plan also fixes the expected scenario set.
type requestMix struct {
	Dataset        string   `json:"dataset"`
	Seed           *int     `json:"seed"`
	InputTokens    *int     `json:"input_tokens"`
	OutputTokens   *int     `json:"output_tokens"`
	RangeRatio     *float64 `json:"range_ratio"`
	PrefixTokens   *int     `json:"prefix_tokens"`
	Prompts        int      `json:"num_prompts"`
	IgnoreEOS      *bool    `json:"ignore_eos"`
	Rate           string   `json:"request_rate"`
	Burstiness     float64  `json:"burstiness"`
	Backend        string   `json:"backend"`
	Endpoint       string   `json:"endpoint"`
	Temperature    *float64 `json:"temperature"`
	Concurrencies  []int    `json:"concurrencies"`
	Repeats        int      `json:"repeats"`
	WarmupRequests int      `json:"warmup_requests"`
}

func importRun(args []string, out, stderr io.Writer) int {
	fs := flag.NewFlagSet("import-vllm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("manifest", "", "run manifest JSON")
	dest := fs.String("out", "", "new normalized benchmark JSON")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	bad := func(err any) int { fmt.Fprintln(stderr, "INVALID:", err); return 2 }
	if *path == "" || *dest == "" || fs.NArg() != 0 {
		return bad("provide -manifest and -out")
	}
	var m manifest
	if err := readJSON(*path, &m); err != nil {
		return bad(err)
	}
	b := benchmark{Workload: m.Workload, Scenarios: map[string]scenario{}, Evidence: &evidence{Format: m.Format, Kind: m.Kind, Contract: m.Contract, Runtime: m.Runtime, Sources: map[string]runSource{}}}
	if strings.TrimSpace(m.Workload) == "" || len(m.Runs) == 0 || m.RequestsPerRun < 100 {
		return bad("manifest requires workload, runs and requests_per_run >= 100")
	}
	repeats := map[int]map[int]bool{}
	if filepath.IsAbs(m.RequestMixFile) || strings.TrimSpace(m.RequestMixFile) == "" {
		return bad("request_mix_file must be relative to manifest")
	}
	mix, err := readBytes(filepath.Join(filepath.Dir(*path), m.RequestMixFile))
	if err != nil {
		return bad(err)
	}
	if digest(mix) != m.Contract.RequestMixSHA256 {
		return bad("request mix file digest differs from contract")
	}
	var plan requestMix
	if err := strictJSON(mix, &plan, true); err != nil {
		return bad(err)
	}
	if plan.Dataset != "random" || plan.Seed == nil || plan.InputTokens == nil || *plan.InputTokens < 1 || plan.OutputTokens == nil || *plan.OutputTokens < 1 || plan.RangeRatio == nil || *plan.RangeRatio != 0 || plan.PrefixTokens == nil || *plan.PrefixTokens != 0 || plan.Prompts != m.RequestsPerRun || plan.IgnoreEOS == nil || !*plan.IgnoreEOS || plan.Rate != "inf" || plan.Burstiness != 1 || plan.Backend != "openai" || plan.Endpoint != "/v1/completions" || plan.Temperature == nil || *plan.Temperature != 0 || plan.Repeats < 3 || plan.WarmupRequests < 1 || len(plan.Concurrencies) == 0 {
		return bad("unsupported or incomplete random workload plan")
	}
	expected := map[int]bool{}
	for _, c := range plan.Concurrencies {
		if c < 1 || expected[c] {
			return bad("plan requires unique positive concurrency limits")
		}
		expected[c] = true
	}
	for _, r := range m.Runs {
		if r.SHA256 != "" {
			return bad("manifest runs must omit sha256; importer computes it")
		}
		name := scenarioName(r)
		if _, ok := b.Scenarios[name]; ok {
			return bad("duplicate scenario/repeat " + name)
		}
		if filepath.IsAbs(r.File) || strings.TrimSpace(r.File) == "" {
			return bad("raw files must be relative to manifest")
		}
		data, err := readBytes(filepath.Join(filepath.Dir(*path), r.File))
		if err != nil {
			return bad(err)
		}
		var v vllmResult
		if err := strictJSON(data, &v, false); err != nil {
			return bad(fmt.Errorf("%s: %w", r.File, err))
		}
		c := m.Contract
		if v.Version != "0.11.0" || v.Kind != m.Kind || v.Workload != m.Workload || v.Mix != c.RequestMixSHA256 || v.Runtime != m.Runtime || v.Hardware != c.Hardware || v.ModelRevision != c.ModelRevision || v.TokenizerRevision != c.TokenizerRevision || v.Warmup != c.Warmup || v.Repeat != strconv.Itoa(r.Repeat) || v.Model != c.Model || v.Tokenizer != c.Tokenizer || v.Backend != "openai" {
			return bad("raw provenance metadata does not match manifest: " + r.File)
		}
		rate, rateOK := v.Rate.(string)
		if v.Prompts == nil || v.Completed == nil || v.Duration == nil || v.Throughput == nil || v.TTFT == nil || v.Concurrency == nil || v.Burstiness == nil || !rateOK || rate != "inf" || *v.Burstiness != 1 || v.Ramp != nil || *v.Concurrency != r.Concurrency || r.Concurrency < 1 || r.Repeat < 1 {
			return bad("raw result requires metrics and fixed saturated traffic (inf rate, burstiness 1, no ramp): " + r.File)
		}
		if *v.Duration <= 0 || *v.Prompts != m.RequestsPerRun || *v.Completed < 0 || *v.Completed > *v.Prompts || math.Abs(*v.Throughput - float64(*v.Completed) / *v.Duration) > 1e-6*math.Max(1, *v.Throughput) {
			return bad("invalid counts, duration or inconsistent request throughput: " + r.File)
		}
		s := scenario{*v.Prompts, *v.Prompts - *v.Completed, *v.TTFT, *v.Throughput}
		if !valid(s, 1) {
			return bad("invalid raw metrics: " + r.File)
		}
		r.SHA256 = digest(data)
		b.Scenarios[name] = s
		b.Evidence.Sources[name] = r
		if repeats[r.Concurrency] == nil {
			repeats[r.Concurrency] = map[int]bool{}
		}
		repeats[r.Concurrency][r.Repeat] = true
	}
	if len(expected) != len(repeats) {
		return bad("manifest scenarios differ from workload plan")
	}
	for concurrency, set := range repeats {
		if !expected[concurrency] || len(set) != plan.Repeats {
			return bad("manifest scenarios/repeats differ from workload plan")
		}
		for i := 1; i <= plan.Repeats; i++ {
			if !set[i] {
				return bad("repeat numbers must be contiguous from 1")
			}
		}
	}
	if err := b.Evidence.validate(b); err != nil {
		return bad(err)
	}
	if err := writeNewJSON(*dest, b); err != nil {
		return bad(err)
	}
	fmt.Fprintf(out, "IMPORTED: %d %s scenario/repeats into %s\n", len(b.Scenarios), m.Kind, *dest)
	return 0
}
