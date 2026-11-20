// Command demo exercises TokenGate's public CLI with synthetic fixtures.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	if err := demo(); err != nil {
		fmt.Fprintln(os.Stderr, "DEMO FAILED:", err)
		os.Exit(1)
	}
}

func demo() error {
	if _, err := os.Stat("examples/vllm/baseline-manifest.json"); err != nil {
		return fmt.Errorf("run from the TokenGate repository root: %w", err)
	}
	buildDir, err := os.MkdirTemp("", "tokengate-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildDir)
	name := "tokengate"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(buildDir, name)
	build := exec.Command("go", "build", "-o", binary, "./cmd/tokengate")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	if err := os.MkdirAll("reports", 0755); err != nil {
		return err
	}
	reports, err := os.MkdirTemp("reports", "demo-")
	if err != nil {
		return err
	}
	base := filepath.Join(reports, "baseline.json")
	candidate := filepath.Join(reports, "candidate.json")
	run := func(label string, expected int, args ...string) error {
		fmt.Printf("\n=== %s ===\n", label)
		cmd := exec.Command(binary, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				return err
			}
			code = exit.ExitCode()
		}
		if code != expected {
			return fmt.Errorf("%s: expected exit %d, got %d", label, expected, code)
		}
		fmt.Printf("Verified exit %d\n", code)
		return nil
	}
	fmt.Println("TokenGate offline demo — synthetic fixtures; no model is run.")
	for _, input := range []struct{ role, path string }{{"baseline", base}, {"candidate", candidate}} {
		if err := run("Import "+input.role, 0, "import-vllm", "-manifest", "examples/vllm/"+input.role+"-manifest.json", "-out", input.path); err != nil {
			return err
		}
	}
	args := func(path, report string) []string {
		return []string{"-baseline", base, "-candidate", path, "-policy", "examples/policy.json", "-report", filepath.Join(reports, report)}
	}
	if err := run("PASS: baseline compared with itself", 0, args(base, "pass.json")...); err != nil {
		return err
	}
	if err := run("BLOCK: one loaded repeat exceeds the latency budget", 1, args(candidate, "block.json")...); err != nil {
		return err
	}
	if err := run("INVALID: synthetic inputs refused for a measured release", 2, append(args(base, "invalid.json"), "-require-measured")...); err != nil {
		return err
	}
	fmt.Printf("\nDEMO VERIFIED: expected PASS (0), BLOCK (1), INVALID (2).\nEvidence saved to %s\n", filepath.ToSlash(reports))
	return nil
}
