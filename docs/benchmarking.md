# Benchmark recipe and evidence contract

## Supported upstream format

Pinned client: vLLM **0.11.0**, upstream commit `b8b302cde434df8c9289a2b465406b47ebab1c2d`.

Primary references:

- [Saved result fields and CLI](https://github.com/vllm-project/vllm/blob/b8b302cde434df8c9289a2b465406b47ebab1c2d/vllm/benchmarks/serve.py): `main_async`, `benchmark`, `calculate_metrics` and `add_cli_args`.
- [Random request sampling](https://github.com/vllm-project/vllm/blob/b8b302cde434df8c9289a2b465406b47ebab1c2d/vllm/benchmarks/datasets.py).
- [Streaming completion request and temperature](https://github.com/vllm-project/vllm/blob/b8b302cde434df8c9289a2b465406b47ebab1c2d/vllm/benchmarks/lib/endpoint_request_func.py).

The importer reads one flat JSON object, not appended JSONL or `.pytorch.json`. Upstream saves requested count as `num_prompts`, successful count as `completed`, successful requests/second as `request_throughput`, elapsed seconds as `duration`, and p95 TTFT in milliseconds as `p95_ttft_ms` when percentile 95 is selected. Failures are requested minus completed. That count contract is appropriate to this fixed random workload; arbitrary datasets can filter samples, so they are intentionally unsupported. TTFT percentiles cover successes. Upstream's per-request `ttfts`, when saved, are seconds; TokenGate does not reinterpret those arrays.

The importer checks `request_throughput` against completed/duration with relative tolerance 1e-6 (absolute floor 1e-6). It requires `backend=openai`, a positive explicit `max_concurrency`, saturated `request_rate="inf"`, burstiness 1 and no ramp. Unneeded upstream fields are tolerated. Required metrics and provenance cannot be missing or null. Duplicate keys, case collisions, trailing JSON, unknown manifest/normalized fields and files over 4 MiB are rejected. Use summary results without `--save-detailed`.

`tokengate_*` fields are **our operator metadata**, supplied through upstream `--metadata`; they are not upstream measurements or an automatically detected vLLM version. The importer cross-checks them against the manifest. Model/tokenizer revisions, hardware, warmup and the exact request-mix file hash must match across baseline and candidate. Runtime descriptions may differ because the intended comparison is a serving runtime/configuration change with fixed weights and workload. Record every changed flag and runtime version there. A different model revision requires a new experimental contract rather than silently comparing it here.

## Running on an existing test server

Prerequisites are a compatible Linux GPU server, already installed vLLM client 0.11.0, pinned local model/tokenizer snapshots and an operator-owned test endpoint. This project installs nothing, downloads no weights and contacts no production endpoint. Use a small model already available to you. Do not infer hardware capacity from this example.

1. Record the immutable model/tokenizer revisions and full server launch command. Start the baseline server with the same local snapshot paths used by the client, for example `vllm serve "$MODEL" --tokenizer "$TOKENIZER" --host 127.0.0.1 --port 8000`. Record effective defaults, dtype, tensor parallelism, scheduler/batching settings, context length, memory utilization, prefix caching and quantization. Use the same settings on the candidate except for the explicitly tested change. Keep client location and network path fixed.
2. Record GPU model/count/VRAM, driver, CUDA, CPU/RAM, power/clock policy, OS, PyTorch and exact server version/commit. Stop competing GPU work. Preserve server logs. Pin a container image digest if using containers; tags alone are insufficient.
3. Set the environment and run the capture script from the repository root:

```sh
export MODEL=/srv/models/pinned-snapshot
export TOKENIZER=/srv/models/pinned-snapshot
export MODEL_REVISION='<immutable model revision>'
export TOKENIZER_REVISION='<immutable tokenizer revision>'
export HARDWARE='<GPU/count/VRAM; CPU/RAM; driver; power policy; host identity>'
export RUNTIME='<server version+commit; CUDA; PyTorch; exact command and flags>'
export BASE_URL=http://127.0.0.1:8000
bash scripts/benchmark.sh reports/raw-baseline
# Stop baseline, start candidate, preserve equivalent environment and update RUNTIME.
bash scripts/benchmark.sh reports/raw-candidate
bin/tokengate import-vllm -manifest reports/raw-baseline/manifest.json -out reports/measured-baseline.json
bin/tokengate import-vllm -manifest reports/raw-candidate/manifest.json -out reports/measured-candidate.json
bin/tokengate -baseline reports/measured-baseline.json -candidate reports/measured-candidate.json -policy examples/policy.json -require-measured -report reports/release-decision.json
```

Create `reports/` first. Every capture directory, normalized output and decision file must be new. If capture stops partway through, keep the partial logs and start again in a new directory; the importer rejects incomplete manifests. Do not append or edit raw benchmark outputs. Metadata and manifests are generated before capture and reviewed by the operator. If raw provenance must change, recapture.

The script checks the installed client version and writes a hashed request plan: 200 requests, deterministic random seed 0, fixed 128 input/32 output tokens, no prefix, ignored EOS, streaming completions, temperature 0, concurrency limits 1 and 16, three repeats each. Before each measured repeat it sends 100 unmeasured warmup requests with the same workload. Upstream also performs a readiness prompt. These are warm-cache measurements; they say nothing about cold start. Warmup logs must show healthy completion before accepting a measured run. Upstream may exit successfully despite individual request failures, so review counts and errors as well as process status.

The script retains six raw JSON objects, warmup logs, measured logs, a manifest and the hashed request plan. vLLM also emits its auxiliary PyTorch-format files; preserve them if useful, but do not import them. The `kind=measured` declaration applies only when the script actually contacted a real inference server. Script syntax and fixture tests are not evidence of a real run.

## Repeats, decisions and limits

Every planned scenario and repeat must be present exactly once. Repeats are numbered contiguously from 1, at least three per concurrency. There is no averaging that could conceal a bad repeat. Gate policy requires at least `min_requests` successful TTFT samples in each nonempty run, in addition to total requested count; all-failed candidates block and an unhealthy baseline is invalid. The sample minimum is an input-quality floor, not a statistical confidence guarantee. For stable p95 claims, increase samples and repeats to suit the observed variability, changing the request plan and capture flags together.

Inspect per-repeat p95, throughput and error counts. Report the min/max range and all paired deltas, not just the best result. The included synthetic loaded baseline p95 range is 390–410 ms; candidate range is 390–635.5 ms. Those invented numbers test repeat isolation and do not characterize any GPU/model. Do not average percentiles or pool summaries as if they were request samples. Run additional alternating baseline/candidate rounds to detect thermal, cache and time-order drift; the compact script captures one side at a time and cannot remove those confounders. Repeating the same seed measures runtime variation for one prompt mix, not diversity across workloads.

A passing decision means only that the declared inputs satisfy the configured thresholds. It does not establish causality, capacity planning, output quality, representative production traffic, end-to-end user experience or a production SLO. Hardware and workload metadata are attestations, not independently measured facts. Hashes detect changed bytes only when compared with a trusted retained copy; they do not authenticate provenance. Preserve raw results, both manifests, request plan, server/client logs, normalized summaries, policy, decision report and the TokenGate commit together. All `reports/` contents are ignored by Git by default.

## Evidence status

No real-model benchmark has been captured for this project. The offline fixtures validate parsing, contracts and gate behavior only. To add measured evidence, run the recipe on an existing compatible test server with vLLM 0.11.0 and pinned local model/tokenizer snapshots, then retain both capture directories and the release decision report. GitHub CI validates the software and synthetic fixtures; it does not run a model.
