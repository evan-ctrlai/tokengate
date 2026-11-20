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

## Next milestone

Normalize output from a pinned vLLM benchmark version. Record hardware, model and runtime versions, workload fingerprint and repeat-run variability. Then publish one real-model comparison and make clear which claims it does and does not support. The current version **does not run inference or claim an observed model regression**.
