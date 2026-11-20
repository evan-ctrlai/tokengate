package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGate(t *testing.T) {
	root := filepath.Join("..", "..", "examples")
	baseline := filepath.Join(root, "baseline.json")
	regression := filepath.Join(root, "candidate-regressed.json")
	good, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, value string
		code              int
		output            string
	}{
		{"pass", baseline, "", 0, "PASS:"},
		{"tail latency blocks despite healthy throughput", regression, "", 1, "p95 TTFT grew 55.00%"},
		{"missing scenario", "", `{"workload":"synthetic-mixed-v1","scenarios":{}}`, 2, "same nonempty scenario set"},
		{"different workload", "", strings.Replace(string(good), "synthetic-mixed-v1", "different-v1", 1), 2, "workload IDs"},
		{"invalid counts", "", strings.Replace(string(good), `"failed": 0`, `"failed": 201`, 1), 2, "invalid or insufficient"},
		{"error budget blocks", "", strings.Replace(string(good), `"failed": 0`, `"failed": 5`, 1), 1, "error rate 2.50%"},
		{"all failed blocks", "", strings.Replace(string(good), `"failed": 0, "p95_ttft_ms": 95, "completed_per_second": 2.0`, `"failed": 200, "p95_ttft_ms": 0, "completed_per_second": 0`, 1), 1, "no successful candidate requests"},
		{"throughput blocks", "", strings.Replace(string(good), `"completed_per_second": 2.0`, `"completed_per_second": 1.0`, 1), 1, "throughput fell 50.00%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if path == "" {
				path = filepath.Join(t.TempDir(), "candidate.json")
				if err := os.WriteFile(path, []byte(tt.value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			got := run([]string{"-baseline", baseline, "-candidate", path, "-policy", filepath.Join(root, "policy.json")}, &out, &out)
			if got != tt.code || !strings.Contains(out.String(), tt.output) {
				t.Fatalf("exit %d; output %s", got, out.String())
			}
		})
	}
}
