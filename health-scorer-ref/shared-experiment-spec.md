# Shared Experiment Spec: Routing Algorithm Parity Comparison

## Purpose

Compare the same routing algorithm (baseline vs evolved) across 4 systems to measure fidelity and improvement consistency.

| System | Owner | Type | Status |
|---|---|---|---|
| (1) Python llm-d | Yevgeny / Ophir | Python inference gateway | Active |
| (2) llm-d (Go EPP) | Yevgeny | Go-based Gateway API Inference Extension | Active |
| (3) BLIS | Inbar / Idan | Simulator | Active |
| (4) Vidur | Orit | Simulator | **Not applicable** — see note |

> **Note on Vidur**: Vidur simulates single-engine performance (batching, scheduling within one vLLM replica) but does not model the distributed orchestration layer (multi-replica routing, KV-cache-aware load balancing, prefix cache routing across workers). Since this experiment measures routing algorithm behavior across multiple workers, Vidur cannot participate. Use `llm-d-inference-sim` instead if a simulation of the orchestration layer is needed.

## What We're Measuring

Each system runs the **same workload** twice:
- **Baseline**: default scoring weights
- **Evolved**: Ophir's SkyDiscover-optimized scoring weights

We compare the **improvement percentage** of the target metric across all 4 systems. If all systems show similar improvement (~13.7%), the systems are faithful proxies of each other.

## Experiment Scope Per System

| System | Runs baseline | Runs evolved | Why |
|---|---|---|---|
| Python llm-d | Yes | Yes | Full Python, supports custom scorers |
| Go EPP | Yes | **No** | No custom scorer support, built-in plugins only |
| BLIS | Yes | Yes | Simulator, supports any scoring logic |

Go EPP only participates in baseline parity. If baseline matches across all systems, the evolved improvement found in Python/BLIS is transferable.

## Common Parameters (identical across all systems, both experiments)

### Scoring Weights — Baseline
```yaml
scorers:
  - type: precise_prefix_cache
    weight: 3.0
  - type: kv_cache_utilization
    weight: 2.0
  - type: queue
    weight: 2.0
picker:
  type: max_score
  top_k: 1
```

### Infrastructure
```yaml
workers: 5
gpu: A100 SXM4 80GB (1 GPU per worker)
cpu: 4, memory: 16Gi per worker
block_size: 64
kv_events: enabled (ZMQ, port 5557)
prefix_caching: enabled
```

---

## Experiment 1: Multi-Turn Trace Replay

### Model
```
meta-llama/Llama-3.1-8B-Instruct
```
- 8B parameters, 128K context window
- HuggingFace gated model — requires accepted license

### Workload
```
File: qwen_traceA_blksz_16.jsonl
Source: https://github.com/alibaba-edu/qwen-bailian-usagetraces-anon/raw/refs/heads/main/qwen_traceA_blksz_16.jsonl
Limit: 5000 entries
```
Multi-turn trace from Alibaba's Qwen production traces. Contains conversation sessions with shared prefixes across turns.

### gpu_memory_utilization
```
0.95 (default)
```

### Benchmark Tool
```
Repo: https://github.com/llm-d/llm-d-benchmark
Script: experimental/multi-turn/production-trace-replay-qwen.py
```
Clone the repo and use the script as-is. No modifications needed.

### Run Command
```bash
python production-trace-replay-qwen.py \
  --model-name "meta-llama/Llama-3.1-8B-Instruct" \
  --base-url "<your-endpoint>" \
  --trace-file "qwen_traceA_blksz_16.jsonl" \
  --limit 5000
```

---

## Experiment 2: SWE-Smith Coding Workload

### Model
```
openai/gpt-oss-20b
```
- 20B parameters, Apache 2.0 (no gating, no login required)
- ~40GB in BF16, fits on 1x A100 80GB

### Workload
```
SWE-smith multi-turn coding trajectories
Data: data/swe_smith/swe_smith_workload.json (in the benchmark repo)
Plan: generated via swe_smith_plan.py (N=40 agents, M=1 trajectory, seed=42)
```
Real SWE-agent coding trajectories with tool calls and growing context. Exercises prefix caching under realistic coding workload.

### gpu_memory_utilization
```
0.80
```

### Benchmark Tool
```
Repo: github.ibm.com/AI4SYS/inference-perf (IBM internal)
Config: examples/vllm/config-swe-smith.yml
```

### Setup
```bash
# 1. Clone
git clone https://github.ibm.com/AI4SYS/inference-perf.git

# 2. Generate replay plan (once, reuse for all runs on all systems)
python scripts/swe_smith_plan.py \
  --data data/swe_smith/swe_smith_workload.json \
  -N 40 -M 1 --seed 42 -o replay_plan.json

# 3. Update config-swe-smith.yml:
#    base_url → your system's endpoint
#    model_name → openai/gpt-oss-20b
#    streaming → true

# 4. Run
inference-perf --config_file examples/vllm/config-swe-smith.yml
```

---

### Scoring Weights — Evolved
```yaml
# From Ophir's SkyDiscover optimization:
# Source: https://github.ibm.com/Video-AI/llm-d-python/blob/ophir-before-vela/evolve_skydiscover/initial_program.py
# (called "initial_program" because further evolution is ongoing)
#
# TODO: Extract exact weights from initial_program.py and list here
# Each team must use identical evolved weights
```

### Scoring Weights — Source Code References
- **Baseline**: https://github.ibm.com/Video-AI/llm-d-python/blob/ophir-before-vela/evolve_skydiscover/initial_program_before_optimization.py
- **Evolved**: https://github.ibm.com/Video-AI/llm-d-python/blob/ophir-before-vela/evolve_skydiscover/initial_program.py

## Run Protocol

### Per configuration (baseline OR evolved)

```
Run 0: warm-up (discard results)
  wait 30 seconds
Run 1: measured
  wait 30 seconds
Run 2: measured
  wait 30 seconds
Run 3: measured
  wait 30 seconds
Run 4: measured
  wait 30 seconds
Run 5: measured
```

Total: 6 runs per configuration, 12 runs per system (baseline + evolved).

### Noise reduction
- **No concurrent workloads** during runs
- **Sequential** — wait for each run to complete before starting next
- **30 second pause** between runs for KV cache stabilization
- **Report median** of 5 measured runs (not mean)

## Pre-flight Checks (MANDATORY before running)

### 1. Inference works
Send a request and get a valid response.

### 2. Prefix cache routing is active
Send the same long prompt (>64 tokens) twice. Verify the second request is routed to the same worker due to cache hit.

**How to verify per system:**
- **Go EPP**: EPP logs show `"scores":{"<pod-ip>":N}` where N > 0
- **Python llm-d**: Gateway logs show `precise_prefix_cache` scorer returning non-zero
- **BLIS/Vidur**: System-specific — confirm prefix cache hit is reflected in routing decision

**DO NOT proceed if prefix cache scores are all zero.** Results would be meaningless.

### 3. Zero failures on a small test
Run with `--limit 50` first. Verify 0 failures.

## System Endpoints

Replace `<your-endpoint>` in run commands with:
- **(1) Python llm-d**: `http://localhost:8080` (gateway)
- **(2) Go EPP**: `http://infra-kv-events-inference-gateway-istio.<namespace>.svc.cluster.local:80`
- **(3) BLIS**: system-specific endpoint

## Metrics (from Ophir's multi-turn-metrics.pdf)

### Top-line metric: Trajectory Time

Trajectory time = sum of latencies across all turns in a conversation. This is the actual wall-clock time a user experiences.

```
trajectory_time = Σ (latency_turn_i) for i = 1..N
where latency_turn_i = TTFT_i + generation_time_i
```

Why this metric:
- Penalizes slow last turns (biggest contributor due to large context)
- Rewards good prefix caching (KV cache hits reduce the sum)
- Rewards good scheduling (efficient routing shrinks wall-clock time)
- Not gameable by fast early turns (unlike averages)

### Per-turn metrics

| Metric | What it measures |
|---|---|
| **Per-turn TTFT** | TTFT for each turn separately (not averaged). Last turn TTFT is what the user feels |
| **Incremental TTFT** | TTFT attributable only to new tokens (isolates prefix cache effectiveness) |
| **TPOT on last turn** | Decode speed under maximum KV cache pressure |
| **E2E latency of last turn** | `TTFT_last + (output_tokens × TPOT_last)` — actual user-facing latency |
| **Prefix cache hit rate** | Whether caching is actually helping |

### Pitfalls to avoid
- Don't use conversation-averaged TTFT as headline — it hides worst-case turns
- Track prefix cache hit rate separately — cache eviction under load causes TTFT spikes
- Report percentiles under realistic concurrency, not single-request
- Fix the workload when comparing — same prompts, same output lengths

## Required Output

After each run, the script saves reports to `./reports/`. Collect:

### 1. Summary metrics (per run)
From `summary_lifecycle_metrics.json`:
- `successes.count` — must be 5000 (0 failures)
- `successes.latency.time_to_first_token` — p50, p90, p99
- `successes.latency.request_latency` — p50, p90, p99
- `successes.throughput.requests_per_sec`
- `successes.throughput.output_tokens_per_sec`
- `failures.count` — must be 0

### 2. TTFT by turn bucket
From console output:
```
Bucket          | Count | P50      | P90      | P99
-------------------------------------------------------
Turn 1          | ...   | ...      | ...      | ...
Turns 2-5       | ...   | ...      | ...      | ...
Turns 6-10      | ...   | ...      | ...      | ...
Turns 11+       | ...   | ...      | ...      | ...
```

### 3. Per-request data
From `per_request_lifecycle_metrics.json` — needed to compute trajectory time (sum latencies per session).

### 4. Post-process with compute_metrics.py

After all runs complete, use `compute_metrics.py` from this repo to compute trajectory time, last-turn metrics, and cross-run medians:

```bash
# Clone this repo (if not already)
git clone https://github.ibm.com/Video-AI/llm-d-deploy.git
cd llm-d-deploy
uv sync   # installs numpy

# Process all runs at once:
python scripts/compute_metrics.py /path/to/experiment_dir/

# Or a single run:
python scripts/compute_metrics.py /path/to/run1/per_request_lifecycle_metrics.json
```

The script outputs:
- Trajectory time (all sessions + multi-turn only) — p50, p90, p99
- Last-turn TTFT, TPOT, E2E latency
- Per-turn TTFT (individual turns)
- TTFT by turn bucket
- Cross-run median summary with variance check (flags >20% spread)

Use this output to fill in the reporting tables below.

## Reporting Format

Each team fills in this table for their system:

### Baseline Results (median of 5 runs)

| Metric | Value |
|---|---|
| Successes | |
| Failures | |
| TTFT p50 (s) | |
| TTFT p90 (s) | |
| TTFT p99 (s) | |
| Request latency p50 (s) | |
| Request latency p90 (s) | |
| Throughput (req/s) | |
| Output tokens/s | |
| TTFT Turn 1 p50 | |
| TTFT Turns 2-5 p50 | |
| TTFT Turns 6-10 p50 | |
| TTFT Turns 11+ p50 | |

### Evolved Results (median of 5 runs)

Same table as above.

### Improvement

| Metric | Baseline | Evolved | Delta | Delta % |
|---|---|---|---|---|
| TTFT p50 | | | | |
| TTFT p90 | | | | |
| Throughput | | | | |
| TTFT Turn 1 p50 | | | | |
| TTFT Turns 2-5 p50 | | | | |
| TTFT Turns 6-10 p50 | | | | |

## Variance Check

For each metric, report min and max across 5 runs. If spread > 20% of median, flag it — indicates instability, not a parity issue.

## What Can Differ Between Systems

- Hardware (GPU type, count) — affects absolute numbers, not relative improvement
- Gateway implementation — transparent to scoring
- Deployment method — irrelevant to benchmark

## What MUST Match Between Systems

- Model (`meta-llama/Llama-3.1-8B-Instruct`)
- Trace file (`qwen_traceA_blksz_16.jsonl`, `--limit 5000`)
- Scoring weights (identical for baseline, identical for evolved)
- Block size (64)
- Worker count (5)
- Run protocol (1 warm-up + 5 measured, 30s pause)
- Benchmark script version (same patched `production-trace-replay-qwen.py`)

## Timeline

| Step | Who | When |
|---|---|---|
| Ophir provides evolved weights | Ophir | TBD |
| System (1) baseline + evolved runs | Yevgeny | After weights |
| System (2) baseline + evolved runs | Yevgeny | After weights |
| System (3) baseline + evolved runs | Inbar / Idan | After weights |
| System (4) baseline + evolved runs | Orit | After weights |
| Compare results | All | After all runs |
