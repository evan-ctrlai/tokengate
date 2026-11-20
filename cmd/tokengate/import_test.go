package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureCopy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join("..", "..", "examples", "vllm")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func editObject(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	edit(value)
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func importFixture(t *testing.T, dir, role string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), role+".json")
	var log bytes.Buffer
	if code := run([]string{"import-vllm", "-manifest", filepath.Join(dir, role+"-manifest.json"), "-out", dest}, &log, &log); code != 0 {
		t.Fatalf("import exit %d: %s", code, &log)
	}
	return dest
}

func TestImportedComparisonAndReport(t *testing.T) {
	dir := fixtureCopy(t)
	base := importFixture(t, dir, "baseline")
	candidate := importFixture(t, dir, "candidate")
	var b benchmark
	if err := readJSON(base, &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Scenarios) != 6 {
		t.Fatal("lost a repeat")
	}
	for name, source := range b.Evidence.Sources {
		raw, err := os.ReadFile(filepath.Join(dir, source.File))
		if err != nil {
			t.Fatal(err)
		}
		if source.SHA256 != digest(raw) {
			t.Fatalf("incorrect raw hash: %s", name)
		}
	}
	for _, tt := range []struct {
		name, path string
		code       int
		decision   string
	}{{"pass", base, 0, "pass"}, {"one repeat blocks", candidate, 1, "block"}} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			var log bytes.Buffer
			policy := filepath.Join("..", "..", "examples", "policy.json")
			code := run([]string{"-baseline", base, "-candidate", tt.path, "-policy", policy, "-report", path}, &log, &log)
			if code != tt.code {
				t.Fatalf("exit %d: %s", code, &log)
			}
			var report gateReport
			if err := readJSON(path, &report); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(tt.path)
			if report.ExitCode != code || report.Decision != tt.decision || report.Inputs["candidate"].SHA256 != digest(data) || report.Output != log.String() || report.Benchmarks["candidate"].Evidence == nil || report.Policy == nil {
				t.Fatalf("incomplete decision evidence: %+v", report)
			}
			if code == 1 && (!strings.Contains(log.String(), "concurrency-16/repeat-2") || !strings.Contains(log.String(), "p95 TTFT grew 55.00%")) {
				t.Fatal(log.String())
			}
		})
	}
}

func TestImportFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*testing.T, string)
	}{
		{"wrong version", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["tokengate_vllm_version"] = "0.12.0" })
		}},
		{"wrong model", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["model_id"] = "other" })
		}},
		{"wrong concurrency", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["max_concurrency"] = 2 })
		}},
		{"count mismatch", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["num_prompts"] = 199 })
		}},
		{"count exceeds requested", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["completed"] = 201 })
		}},
		{"inconsistent throughput", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["request_throughput"] = 40 })
		}},
		{"object request rate cannot panic", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["request_rate"] = map[string]any{"rate": "inf"} })
		}},
		{"finite rate unsupported", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["request_rate"] = 10 })
		}},
		{"ramp unsupported", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { v["ramp_up_strategy"] = "linear" })
		}},
		{"changed request mix", func(t *testing.T, d string) {
			if err := os.WriteFile(filepath.Join(d, "request-mix.json"), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing repeat", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-manifest.json"), func(v map[string]any) { v["runs"] = v["runs"].([]any)[1:] })
		}},
		{"duplicate repeat", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-manifest.json"), func(v map[string]any) { a := v["runs"].([]any); v["runs"] = append(a, a[0]) })
		}},
		{"missing raw metadata", func(t *testing.T, d string) {
			editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) { delete(v, "tokengate_hardware") })
		}},
		{"trailing object", func(t *testing.T, d string) {
			p := filepath.Join(d, "baseline-c1-r1.json")
			b, _ := os.ReadFile(p)
			if err := os.WriteFile(p, append(b, []byte("\n{}")...), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate metric", func(t *testing.T, d string) {
			p := filepath.Join(d, "baseline-c1-r1.json")
			b, _ := os.ReadFile(p)
			b = bytes.Replace(b, []byte(`"completed": 200`), []byte(`"completed": 200,"COMPLETED": 200`), 1)
			if err := os.WriteFile(p, b, 0600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, field := range []string{"num_prompts", "completed", "duration", "request_throughput", "p95_ttft_ms", "max_concurrency", "burstiness"} {
		for _, null := range []bool{false, true} {
			field, null := field, null
			tests = append(tests, struct {
				name string
				edit func(*testing.T, string)
			}{field + map[bool]string{false: " missing", true: " null"}[null], func(t *testing.T, d string) {
				editObject(t, filepath.Join(d, "baseline-c1-r1.json"), func(v map[string]any) {
					if null {
						v[field] = nil
					} else {
						delete(v, field)
					}
				})
			}})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fixtureCopy(t)
			tt.edit(t, dir)
			dest := filepath.Join(dir, "normalized.json")
			var log bytes.Buffer
			if code := run([]string{"import-vllm", "-manifest", filepath.Join(dir, "baseline-manifest.json"), "-out", dest}, &log, &log); code != 2 {
				t.Fatalf("exit %d: %s", code, &log)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("invalid import left an output")
			}
		})
	}
}

func TestGateEvidenceAndSamplesFailClosed(t *testing.T) {
	dir := fixtureCopy(t)
	base := importFixture(t, dir, "baseline")
	for _, tt := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"different hardware", func(v map[string]any) {
			v["evidence"].(map[string]any)["contract"].(map[string]any)["hardware"] = "different GPU"
		}},
		{"different kind", func(v map[string]any) { v["evidence"].(map[string]any)["kind"] = "measured" }},
		{"missing provenance", func(v map[string]any) { delete(v, "evidence") }},
		{"missing scenario", func(v map[string]any) { delete(v["scenarios"].(map[string]any), "concurrency-1/repeat-1") }},
		{"too few successes", func(v map[string]any) {
			v["scenarios"].(map[string]any)["concurrency-1/repeat-1"].(map[string]any)["failed"] = 150
		}},
		{"different requests", func(v map[string]any) {
			v["scenarios"].(map[string]any)["concurrency-1/repeat-1"].(map[string]any)["requests"] = 300
		}},
		{"missing failures", func(v map[string]any) {
			delete(v["scenarios"].(map[string]any)["concurrency-1/repeat-1"].(map[string]any), "failed")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, _ := os.ReadFile(base)
			candidate := filepath.Join(t.TempDir(), "candidate.json")
			if err := os.WriteFile(candidate, data, 0600); err != nil {
				t.Fatal(err)
			}
			editObject(t, candidate, tt.edit)
			var log bytes.Buffer
			code := run([]string{"-baseline", base, "-candidate", candidate, "-policy", filepath.Join("..", "..", "examples", "policy.json")}, &log, &log)
			if code != 2 {
				t.Fatalf("exit %d: %s", code, &log)
			}
		})
	}
	var log bytes.Buffer
	if code := run([]string{"-baseline", base, "-candidate", base, "-policy", filepath.Join("..", "..", "examples", "policy.json"), "-require-measured"}, &log, &log); code != 2 {
		t.Fatalf("synthetic input passed release mode: %s", &log)
	}
}

func TestOutputNeverOverwritesEvidence(t *testing.T) {
	dir := fixtureCopy(t)
	path := filepath.Join(dir, "baseline-manifest.json")
	before, _ := os.ReadFile(path)
	var log bytes.Buffer
	if code := run([]string{"import-vllm", "-manifest", path, "-out", path}, &log, &log); code != 2 {
		t.Fatal("overwrote manifest")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("manifest changed")
	}
	base := importFixture(t, dir, "baseline")
	before, _ = os.ReadFile(base)
	if code := run([]string{"-baseline", base, "-candidate", base, "-policy", filepath.Join("..", "..", "examples", "policy.json"), "-report", base}, &log, &log); code != 2 {
		t.Fatal("report output collision did not invalidate")
	}
	after, _ = os.ReadFile(base)
	if !bytes.Equal(before, after) {
		t.Fatal("benchmark changed")
	}
}

func TestInvalidReportAndMeasuredRequirement(t *testing.T) {
	dir := fixtureCopy(t)
	base := importFixture(t, dir, "baseline")
	policy := filepath.Join("..", "..", "examples", "policy.json")
	reportPath := filepath.Join(t.TempDir(), "invalid.json")
	var log bytes.Buffer
	if code := run([]string{"-baseline", base, "-candidate", base, "-policy", policy, "-require-measured", "-report", reportPath}, &log, &log); code != 2 {
		t.Fatalf("exit %d: %s", code, &log)
	}
	var report gateReport
	if err := readJSON(reportPath, &report); err != nil {
		t.Fatal(err)
	}
	if report.Decision != "invalid" || report.ExitCode != 2 || report.Inputs["baseline"].SHA256 == "" {
		t.Fatalf("invalid report: %+v", report)
	}
	// Unit-test the declared kind requirement; these bytes remain synthetic.
	editObject(t, base, func(v map[string]any) { v["evidence"].(map[string]any)["kind"] = "measured" })
	log.Reset()
	if code := run([]string{"-baseline", base, "-candidate", base, "-policy", policy, "-require-measured"}, &log, &log); code != 0 {
		t.Fatalf("declared measured evidence exit %d: %s", code, &log)
	}
}

func TestLegacyMissingMetricsAndNullPolicy(t *testing.T) {
	baseline := filepath.Join("..", "..", "examples", "baseline.json")
	policyPath := filepath.Join("..", "..", "examples", "policy.json")
	for _, field := range []string{"requests", "failed", "p95_ttft_ms", "completed_per_second"} {
		for _, null := range []bool{false, true} {
			t.Run(field+map[bool]string{true: " null", false: " absent"}[null], func(t *testing.T) {
				data, _ := os.ReadFile(baseline)
				path := filepath.Join(t.TempDir(), "candidate.json")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				editObject(t, path, func(v map[string]any) {
					s := v["scenarios"].(map[string]any)["concurrency-1"].(map[string]any)
					if null {
						s[field] = nil
					} else {
						delete(s, field)
					}
				})
				var log bytes.Buffer
				if code := run([]string{"-baseline", baseline, "-candidate", path, "-policy", policyPath}, &log, &log); code != 2 {
					t.Fatalf("exit %d: %s", code, &log)
				}
			})
		}
	}
	for _, field := range []string{"min_requests", "max_p95_ttft_growth_pct", "max_throughput_drop_pct", "max_candidate_error_pct"} {
		t.Run("null policy "+field, func(t *testing.T) {
			data, _ := os.ReadFile(policyPath)
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			editObject(t, path, func(v map[string]any) { v[field] = nil })
			var log bytes.Buffer
			if code := run([]string{"-baseline", baseline, "-candidate", baseline, "-policy", path}, &log, &log); code != 2 {
				t.Fatalf("exit %d: %s", code, &log)
			}
		})
	}
}
