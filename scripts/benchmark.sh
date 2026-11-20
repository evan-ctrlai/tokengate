#!/usr/bin/env bash
# Run only on an existing, operator-owned vLLM test server. No installs/downloads.
set -euo pipefail
if [[ $# != 1 ]]; then echo 'Usage: scripts/benchmark.sh NEW_OUTPUT_DIRECTORY' >&2; exit 2; fi
: "${MODEL:?Set MODEL to an existing pinned model snapshot path}"
: "${TOKENIZER:?Set TOKENIZER to an existing pinned tokenizer snapshot path}"
: "${MODEL_REVISION:?Set MODEL_REVISION to the immutable model revision}"
: "${TOKENIZER_REVISION:?Set TOKENIZER_REVISION to the immutable tokenizer revision}"
: "${HARDWARE:?Record GPU type/count/VRAM, host CPU/RAM, power limits and driver}"
: "${RUNTIME:?Record server version/commit, CUDA/PyTorch, full server flags and config}"
python3 -c 'import importlib.metadata as m; assert m.version("vllm")=="0.11.0", "requires vLLM client 0.11.0"'
mkdir "$1" # Deliberately fails if the evidence directory already exists.
export TOKENGATE_OUTPUT="$1"
export TOKENGATE_WARMUP='100 unmeasured requests before each repeat plus upstream readiness probe; warm cache'
python3 - <<'PY'
import hashlib,json,os,pathlib
p=pathlib.Path(os.environ['TOKENGATE_OUTPUT'])
mix={'dataset':'random','seed':0,'input_tokens':128,'output_tokens':32,
     'range_ratio':0.0,'prefix_tokens':0,'num_prompts':200,'ignore_eos':True,
     'request_rate':'inf','burstiness':1,'backend':'openai','endpoint':'/v1/completions',
     'temperature':0,'concurrencies':[1,16],'repeats':3,'warmup_requests':100}
data=(json.dumps(mix,sort_keys=True,indent=2)+'\n').encode()
(p/'request-mix.json').write_bytes(data)
contract={'model':os.environ['MODEL'],'model_revision':os.environ['MODEL_REVISION'],
          'tokenizer':os.environ['TOKENIZER'],'tokenizer_revision':os.environ['TOKENIZER_REVISION'],
          'hardware':os.environ['HARDWARE'],'request_mix_sha256':hashlib.sha256(data).hexdigest(),
          'warmup':os.environ['TOKENGATE_WARMUP']}
m={'format':'vllm-bench-serve/0.11.0','kind':'measured','workload':'random-128-32-seed0-v1',
   'contract':contract,'runtime':os.environ['RUNTIME'],'requests_per_run':200,
   'request_mix_file':'request-mix.json',
   'runs':[{'file':f'c{c}-r{r}.json','concurrency':c,'repeat':r} for c in (1,16) for r in (1,2,3)]}
(p/'manifest.json').write_text(json.dumps(m,indent=2)+'\n')
PY
mix_sha=$(python3 -c 'import json,os; print(json.load(open(os.path.join(os.environ["TOKENGATE_OUTPUT"],"manifest.json")))["contract"]["request_mix_sha256"])')
common=(--backend openai --base-url "${BASE_URL:-http://127.0.0.1:8000}" --endpoint /v1/completions
        --model "$MODEL" --tokenizer "$TOKENIZER" --dataset-name random --seed 0
        --random-input-len 128 --random-output-len 32 --random-range-ratio 0 --random-prefix-len 0
        --ignore-eos --request-rate inf --burstiness 1 --percentile-metrics ttft --metric-percentiles 95)
# v0.11.0's OpenAI completion request function sets temperature=0 by default.
for c in 1 16; do
  for r in 1 2 3; do
    vllm bench serve "${common[@]}" --max-concurrency "$c" --num-prompts 100 > "$1/warmup-c$c-r$r.log" 2>&1
    vllm bench serve "${common[@]}" --max-concurrency "$c" --num-prompts 200 \
      --save-result --result-dir "$1" --result-filename "c$c-r$r.json" \
      --metadata tokengate_vllm_version=0.11.0 tokengate_kind=measured \
      tokengate_workload=random-128-32-seed0-v1 "tokengate_request_mix_sha256=$mix_sha" \
      "tokengate_runtime=$RUNTIME" "tokengate_hardware=$HARDWARE" \
      "tokengate_model_revision=$MODEL_REVISION" "tokengate_tokenizer_revision=$TOKENIZER_REVISION" \
      "tokengate_warmup=$TOKENGATE_WARMUP" "tokengate_repeat=$r" > "$1/c$c-r$r.log" 2>&1
  done
done
echo "Captured six measured runs in $1; review logs, then import manifest.json."
