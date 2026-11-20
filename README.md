# TokenGate

**Catch inference performance regressions before promotion.**

TokenGate is a small Go CLI that compares benchmark summaries for a known-good LLM server and a rollout candidate. It evaluates every load scenario independently, so fast single-user results do not hide regressions under concurrency.

## Run the offline demo

Requires Go 1.23+. No external Go modules, GPU, model, or network access after installing Go.

```sh
go build -o bin/tokengate ./cmd/tokengate
./bin/tokengate -baseline examples/baseline.json -candidate examples/candidate-regressed.json -policy examples/policy.json
```

The included **synthetic fixture** improves p95 time to first token (TTFT) at concurrency 1 and leaves loaded throughput intact, but under concurrency 16 p95 TTFT rises from 400 ms to 620 ms (+55%). The gate blocks the candidate. Use the baseline as both inputs to see the passing path.

- Exit **0**: all scenarios passed.
- Exit **1**: valid evidence, release thresholds breached.
- Exit **2**: invalid, incomplete, or incomparable input; do not promote.

## Evidence contract

Use the same workload definition, prompt mix, token caps, benchmark settings, warmup and hardware. The workload ID and scenario names must match; the gate cannot independently prove the runs used the same configuration. Throughput is completed requests per second; TTFT refers to successful responses. Failed and timed-out requests go in the failed count, not the latency percentiles. A baseline already over the error budget is not an acceptable control. The supplied limits are illustrative, not recommended SLOs.

```sh
go test ./...
go vet ./...
```

## Import a reproducible vLLM comparison

The importer supports the flat `vllm bench serve --save-result` summary from **vLLM 0.11.0**, with explicit TokenGate metadata. It preserves each concurrency/repeat independently and hashes every raw file. Three or more repeats and at least 100 requested samples per run are required. Missing/null metrics, mismatched metadata, unexpected scenarios, inconsistent throughput and incomplete evidence fail closed.

```sh
mkdir -p reports
bin/tokengate import-vllm -manifest examples/vllm/baseline-manifest.json -out reports/baseline.json
bin/tokengate import-vllm -manifest examples/vllm/candidate-manifest.json -out reports/candidate.json
bin/tokengate -baseline reports/baseline.json -candidate reports/baseline.json -policy examples/policy.json -report reports/pass.json
bin/tokengate -baseline reports/baseline.json -candidate reports/candidate.json -policy examples/policy.json -report reports/block.json
# Last command deliberately exits 1: concurrency 16, repeat 2 regresses by 55%.
```

These are **hand-authored synthetic fixtures**, shaped like upstream summary output. No model was run. Five candidate repeats are healthy; one repeat blocks promotion. The JSON report retains the decision, exact input SHA-256 hashes, normalized data, raw hashes, provenance and policy. Output paths must be new; existing evidence is never overwritten. Reports are audit records, not signed attestations.

For measured evidence, use the [benchmark recipe and methodology](docs/benchmarking.md). Release automation should require measured inputs:

```sh
bin/tokengate -baseline reports/measured-baseline.json -candidate reports/measured-candidate.json -policy examples/policy.json -require-measured -report reports/release-decision.json
```

`-require-measured` rejects synthetic fixtures and legacy summaries. A `measured` label is an operator declaration; TokenGate cannot establish that a model or claimed environment actually produced the files. Keep the raw files and logs with the report.

## Validation

The [GitHub Actions workflow](.github/workflows/ci.yml) builds, tests, vets and exercises original and imported pass/block paths without model access or Go module downloads. Actions themselves and Go setup need GitHub network access. It uploads synthetic decision reports. A green job validates the software and fixtures; it establishes no inference performance claim.

The current implementation compares TTFT, successful requests/second and errors. It does not run inference by itself, measure quality, pool percentile samples, estimate statistical significance or establish production SLOs. The next evidence milestone is a controlled real-model comparison with retained raw results.
