package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
)

const vllmFormat = "vllm-bench-serve/0.11.0"

// Contract is supplied by the operator: upstream summary files do not record
// model revisions, hardware or the full workload configuration themselves.
type contract struct {
	Model             string `json:"model"`
	ModelRevision     string `json:"model_revision"`
	Tokenizer         string `json:"tokenizer"`
	TokenizerRevision string `json:"tokenizer_revision"`
	Hardware          string `json:"hardware"`
	RequestMixSHA256  string `json:"request_mix_sha256"`
	Warmup            string `json:"warmup"`
}

type runSource struct {
	File        string `json:"file"`
	SHA256      string `json:"sha256"`
	Concurrency int    `json:"concurrency"`
	Repeat      int    `json:"repeat"`
}

type evidence struct {
	Format   string               `json:"format"`
	Kind     string               `json:"kind"`
	Contract contract             `json:"contract"`
	Runtime  string               `json:"runtime"`
	Sources  map[string]runSource `json:"sources"`
}

type manifest struct {
	Format         string      `json:"format"`
	Kind           string      `json:"kind"`
	Workload       string      `json:"workload"`
	Contract       contract    `json:"contract"`
	Runtime        string      `json:"runtime"`
	RequestsPerRun int         `json:"requests_per_run"`
	RequestMixFile string      `json:"request_mix_file"`
	Runs           []runSource `json:"runs"`
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}

func (e evidence) validate(b benchmark) error {
	c := e.Contract
	for _, s := range []string{c.Model, c.ModelRevision, c.Tokenizer, c.TokenizerRevision, c.Hardware, c.Warmup, e.Runtime} {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("evidence requires model/tokenizer revisions, hardware, warmup and runtime")
		}
	}
	if e.Format != vllmFormat || (e.Kind != "synthetic" && e.Kind != "measured") || !validDigest(c.RequestMixSHA256) || len(e.Sources) != len(b.Scenarios) || len(e.Sources) == 0 {
		return fmt.Errorf("invalid evidence format, kind, request mix digest or sources")
	}
	for name, source := range e.Sources {
		if _, ok := b.Scenarios[name]; !ok || !validDigest(source.SHA256) || strings.TrimSpace(source.File) == "" || source.Concurrency < 1 || source.Repeat < 1 || name != scenarioName(source) {
			return fmt.Errorf("invalid evidence source %q", name)
		}
	}
	return nil
}

func compareEvidence(b, c benchmark) error {
	if b.Evidence == nil && c.Evidence == nil {
		return nil
	}
	if b.Evidence == nil || c.Evidence == nil {
		return fmt.Errorf("both inputs require evidence when either has it")
	}
	for _, input := range []benchmark{b, c} {
		if err := input.Evidence.validate(input); err != nil {
			return err
		}
	}
	if b.Evidence.Kind != c.Evidence.Kind || !reflect.DeepEqual(b.Evidence.Contract, c.Evidence.Contract) {
		return fmt.Errorf("evidence kinds or workload/environment contracts differ")
	}
	return nil
}

func scenarioName(r runSource) string {
	return fmt.Sprintf("concurrency-%d/repeat-%d", r.Concurrency, r.Repeat)
}

// No output file is overwritten. A failed write removes its incomplete output.
func writeNewJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// strictJSON rejects duplicate object keys (including nested objects), trailing
// values and oversized input before Go's usual struct validation.
func strictJSON(data []byte, dst any, unknown bool) error {
	if len(data) > 4<<20 {
		return fmt.Errorf("JSON exceeds 4 MiB limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					s := strings.ToLower(key.(string))
					if keys[s] {
						return fmt.Errorf("duplicate JSON field %q", s)
					}
					keys[s] = true
					if err := walk(); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unexpected JSON delimiter")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON data")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	if unknown {
		d.DisallowUnknownFields()
	}
	return d.Decode(dst)
}

// Missing or null numbers must not silently become zero failures or latency.
func (s *scenario) UnmarshalJSON(data []byte) error {
	type fields struct {
		Requests   *int     `json:"requests"`
		Failed     *int     `json:"failed"`
		TTFT       *float64 `json:"p95_ttft_ms"`
		Throughput *float64 `json:"completed_per_second"`
	}
	var f fields
	if err := strictJSON(data, &f, true); err != nil {
		return err
	}
	if f.Requests == nil || f.Failed == nil || f.TTFT == nil || f.Throughput == nil {
		return fmt.Errorf("scenario requires requests, failed, p95_ttft_ms and completed_per_second")
	}
	*s = scenario{*f.Requests, *f.Failed, *f.TTFT, *f.Throughput}
	return nil
}

type inputReceipt struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type gateReport struct {
	Version    int                     `json:"version"`
	ExitCode   int                     `json:"exit_code"`
	Decision   string                  `json:"decision"`
	Inputs     map[string]inputReceipt `json:"inputs"`
	Output     string                  `json:"output"`
	Benchmarks map[string]benchmark    `json:"benchmarks,omitempty"`
	Policy     *policy                 `json:"policy,omitempty"`
}
