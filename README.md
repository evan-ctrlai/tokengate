# TokenGate

**A release gate for self-hosted LLM serving changes.**

A candidate can look faster with one user and still make people wait longer under load. TokenGate compares a known-good baseline with a candidate, checks each load scenario and repeat against a policy, and returns a promotion decision with the evidence behind it. Averaging runs could hide the one that regressed.

| Verified synthetic example | Baseline p95 time to first token | Candidate | Decision |
| --- | ---: | ---: | --- |
| Concurrency 1, repeat 2 | 96 ms | 86 ms | Within budget |
| Concurrency 16, repeat 2 | 410 ms | 635.5 ms (+55%) | **BLOCK**: 20% growth budget exceeded |

The other five candidate repeats pass. One breach still blocks promotion. **These are invented fixture values, not measurements of a model.** Real vLLM server performance is unmeasured in this project.

## Try it in one command

Requires installed **Git and Go 1.23+**. Clone the review branch, then run the demo; the same commands work in PowerShell, Bash and zsh:

```sh
git clone --branch feature/vllm-evidence-gate https://github.com/evan-ctrlai/tokengate.git
cd tokengate
go run ./cmd/demo
```

No credentials, GPU, model, Python or external Go modules are required. The demo builds the CLI, imports the synthetic vLLM-shaped results, checks expected **PASS (exit 0)** and **BLOCK (exit 1)** decisions, and verifies that release mode rejects synthetic evidence with **INVALID (exit 2)**. The demo command itself exits 0 only when all expected outcomes occur. It saves a fresh evidence directory under `reports/demo-*` on each run, so rerunning it preserves earlier reports.

Selected lines from the verified output:

```text
PASS: candidate met release policy
Verified exit 0
  BLOCK: p95 TTFT grew 55.00% (budget 20.00%)
BLOCK: candidate failed release policy
Verified exit 1
INVALID: release mode requires measured evidence on both inputs
Verified exit 2
DEMO VERIFIED: expected PASS (0), BLOCK (1), INVALID (2).
```

## What this adds to vLLM bench

vLLM bench collects performance results. TokenGate turns comparable results into a repeatable release decision:

1. **Import:** accept the documented flat summary from vLLM 0.11.0, validate the workload plan and provenance, and hash the raw files.
2. **Compare:** match scenarios/repeats and require adequate samples and a healthy baseline. Evaluate p95 TTFT, successful requests/second and error budgets for every repeat.
3. **Decide:** return exit 0/1/2 and an optional JSON report containing input hashes, normalized results, raw-file hashes, provenance and policy. CI can use that exit code to stop promotion.

The [Go gate](cmd/tokengate/main.go), [importer](cmd/tokengate/import.go), [evidence validation](cmd/tokengate/evidence.go) and [tests](cmd/tokengate/import_test.go) use the standard library. There is no service to deploy. Existing output files are never overwritten.

## Run the gate directly

To inspect the CLI without the demo wrapper, build it and run the original, smaller synthetic fixtures:

```sh
go build -o bin/tokengate ./cmd/tokengate
# PASS — exits 0
./bin/tokengate -baseline examples/baseline.json -candidate examples/baseline.json -policy examples/policy.json
# BLOCK — deliberately exits 1
./bin/tokengate -baseline examples/baseline.json -candidate examples/candidate-regressed.json -policy examples/policy.json
```

This smaller fixture has a 400 → 620 ms loaded p95 regression; the repeat demo above uses the separate [vLLM-shaped fixtures](examples/vllm). The [policy](examples/policy.json) is illustrative, not a recommended SLO. With `-report <new-file.json>`, the gate also records its decision and evidence. Exit 2 means invalid, incomplete or incomparable input: do not promote.

## Evidence and limits

The importer requires a complete workload plan, at least three repeats per concurrency, at least 100 requested samples per run, matching provenance and consistent metrics. The gate requires the policy's minimum successful TTFT sample count in each nonempty run. A baseline already over the error budget is invalid; an all-failed candidate blocks. Percentiles are never pooled or averaged across runs.

All included results are **hand-authored synthetic fixtures**. The [GitHub Actions workflow](.github/workflows/ci.yml) verifies build, tests, vet, the one-command demo, and the original/imported pass/block paths. It retains synthetic decision reports. Green CI proves these software paths work; it establishes no inference speed, capacity or production-readiness claim. Actions and Go setup need GitHub network access; the checks need no model access or Go module downloads.

For a real comparison, follow the [pinned vLLM capture recipe and methodology](docs/benchmarking.md). It covers model/tokenizer revisions, request mix, hardware/runtime, warmup, repeat variation and retained raw evidence. Release automation should pass `-require-measured` to reject synthetic and legacy summaries. A `measured` label is an operator declaration; hashes do not authenticate provenance or prove the claimed configuration was used.

This project gates TTFT, throughput and errors. It does not measure output quality, infer statistical significance, prove causality or reproduce production traffic. The remaining evidence milestone is a controlled real-model baseline/candidate comparison. No measured regression is claimed.

## Development

```sh
go test ./...
go vet ./...
```
