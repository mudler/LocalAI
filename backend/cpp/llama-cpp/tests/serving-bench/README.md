# Serving benchmark

Scripts for the A/B of the paged serving options of the llama-cpp backend
(`paged_kv`, `prefill_budget`, `kv_output_reserve`, `paged_preempt`). Standard
library Python only. They talk to `/v1/completions`, so they run against a
LocalAI instance or against a plain `llama-server`.

- `serve_bench.py` replays fixed request traces and summarizes them.
- `parity.py` compares greedy outputs of two arms, with a near-tie rule.

The CI checks are the `paged_*` specs of `tests/e2e-backends`
(`make test-extra-backend-llama-cpp-paged`). They assert only large effects.
This directory is for the full, manual A/B.

## Arms

Give each arm its own model YAML with the same GGUF file, so one LocalAI
instance serves all arms. Start LocalAI with `--single-active-backend` so only
one arm holds memory at a time.

| Arm | Options |
|---|---|
| A0 | A backend built without the patches (the unpatched llama.cpp pin), for example as an external gRPC backend |
| A | The patched backend with paged serving off: `paged_kv:false`, `prefill_budget:0` |
| B0 | `paged_kv`, `paged_preempt:false`, `prefill_budget:0` |
| B1 | B0 plus `prefill_budget:256` |
| B2 | B0 plus `prefill_budget:512` |
| B3 | `paged_kv`, `kv_output_reserve:64`, `prefill_budget:0` (preemption on) |

Example model file for arm B1 of the mixed workload:

```yaml
name: b1-mixed
backend: llama-cpp
context_size: 16384
parameters:
  model: Qwen3-0.6B-Q8_0.gguf
options:
  - paged_preempt:false
  - prefill_budget:256
  - parallel:5
```

## Workloads

| Workload | Trace | Pool and slots |
|---|---|---|
| overcommit | `gen --workload overcommit`: 8 requests of about 600 prompt and 200 output tokens at t=0 | `context_size: 2048`, `parallel:4` |
| overcommit-preempt | `gen --workload overcommit --max-tokens 400` | same |
| mixed | `gen --workload mixed`: 4 chat streams, one 3.6k-token prompt at t=3 s | `context_size: 16384`, `parallel:5` |
| steady | `gen --workload steady`: 24 Poisson arrivals, 1 per second | `context_size: 8192`, `parallel:4` |
| shared | `gen --workload shared`: steady with a common 300-token system prompt | same |
| single | `gen --workload single`: 6 serial requests | `context_size: 4096`, `parallel:1` |
| concurrent N | `gen --workload overcommit --n N --words 300 --max-tokens 256`: N requests at t=0 that fit the pool | `context_size: 32768`, `parallel:N` |

| natural | `gen --workload natural --n N`: N instruction prompts at once that stop at EOS (chat-like output; the other traces ignore EOS, which can make a small model loop and flatters n-gram drafting) | `parallel:N` |
| hol | `gen --workload hol`: one long prompt, then short requests right behind it (head-of-line blocking) | small pool, 4 slots |
| collide | `gen --workload collide --words W`: 4 prompts warmed one by one, then sent again together with 4 new prompts of the same length (cache-aware order) | 4 slots, `--cache-prompt` |
| pressure | `gen --workload pressure --words W`: conversations whose second turn comes back after others filled the pool (idle-slot eviction) | small pool, `--cache-prompt` |

`run` and `ab` take `--cache-prompt` to let the server reuse cached prefixes;
`sum --by-kind` adds the TTFT of each request kind.

On a GPU, scale the mixed workload up, for example `--chats 8 --long-words 8000`
with `context_size: 65536` and `parallel:9`.

All requests are greedy, with `ignore_eos`, so every arm generates the same
number of tokens.

## Running

```bash
python3 serve_bench.py gen --workload mixed --seed 1 > trace-mixed.jsonl
python3 serve_bench.py ab --url http://127.0.0.1:8080 --trace trace-mixed.jsonl \
  --workload mixed --reps 5 --out req-mixed.jsonl \
  A0=a0-mixed A=a-mixed B0=b0-mixed B1=b1-mixed B2=b2-mixed
python3 serve_bench.py sum req-mixed.jsonl --ref A
```

`ab` interleaves the arms (rep 0 of every arm, then rep 1, ...) and sends one
warmup request at each arm switch, so a slow period on a shared host hits all
arms. `sum` prints the median over reps of each metric and, with `--ref`, the
paired difference to the reference arm with a bootstrap 95% confidence
interval. Treat an effect as real only when the interval excludes 0 and the
effect is larger than the noise floor: the larger of 3% and twice the spread
of an A/A run.

Count the paged events in the backend log: `waits = N` (admission waits),
`preemptions = N` (resume by recompute) and `aborts = N` (last-resort aborts of
requests that cannot be resumed).

## Parity

```bash
python3 parity.py run --url http://127.0.0.1:8080 --model a-overcommit --mode serial --out a.json
python3 parity.py run --url http://127.0.0.1:8080 --model b3-overcommit --mode concurrent --out b3.json
python3 parity.py cmp a.json b3.json
```

A stream passes when its text is identical, or when it first differs at a
token where the reference's top-1 and top-2 log-probabilities are less than
0.1 nats apart (a near tie that a different batch can flip). `cmp` fails
unless every stream passes and at least 95% are identical.
