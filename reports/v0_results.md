# BLIS Experiment Results — Health Scorer Parity

## Experiment Setup

### Infrastructure

| Parameter | Value |
|-----------|-------|
| Simulator | BLIS (Blackbox Inference Simulator) — deterministic DES |
| Model | `mistralai/mistral-small-24b-instruct-2501` |
| Hardware | H100, TP=1 |
| Instances | 5 |
| Block size | 64 tokens (`--block-size-in-tokens 64`) |
| Latency model | `blackbox` (trained coefficients from `defaults.yaml`) |
| Routing policy | `weighted` |

### Workload

| Parameter | Value |
|-----------|-------|
| Source | `swe_smith_workload.json` — SWE-agent coding trajectories |
| Converter | `blis convert swe-smith` |
| Sessions | 198 |
| Total turns (requests) | 3,837 |
| Turn gap | 30,000 ms (synthesized arrival times, `--turn-gap-ms 30000`) |
| Trace header | `traces/swe_smith.yaml` |
| Trace data | `traces/swe_smith.csv` |

### Scorer Configurations

**Baseline:**
```
--routing-scorers prefix-affinity:3,kv-utilization:2,queue-depth:2
```

**Evolved:**
```
--routing-scorers prefix-affinity:4.0,active-requests:3.8,health:3.5
```

Scorer mapping from reference → BLIS:

| Reference (llm-d) | BLIS | Weight |
|-------------------|------|--------|
| `precise_prefix_cache` | `prefix-affinity` | 4.0 |
| `active_request` | `active-requests` | 3.8 |
| `health_scorer` | `health` | 3.5 |
| `no_hit_lru` | *(dropped — no BLIS equivalent, ~5% weight)* | — |

### Run Commands

```bash
# Baseline
./blis replay \
  --trace-header traces/swe_smith.yaml \
  --trace-data traces/swe_smith.csv \
  --model mistralai/mistral-small-24b-instruct-2501 \
  --hardware H100 --tp 1 --num-instances 5 \
  --block-size-in-tokens 64 \
  --routing-policy weighted \
  --routing-scorers prefix-affinity:3,kv-utilization:2,queue-depth:2 \
  --latency-model blackbox \
  --results-path traces/baseline_results.json

# Evolved
./blis replay \
  --trace-header traces/swe_smith.yaml \
  --trace-data traces/swe_smith.csv \
  --model mistralai/mistral-small-24b-instruct-2501 \
  --hardware H100 --tp 1 --num-instances 5 \
  --block-size-in-tokens 64 \
  --routing-policy weighted \
  --routing-scorers prefix-affinity:4.0,active-requests:3.8,health:3.5 \
  --latency-model blackbox \
  --results-path traces/evolved_results.json
```

---

## Results

### Cluster-Level Metrics

| Metric | Baseline | Evolved | Delta |
|--------|----------|---------|-------|
| Completed requests | 3,837 | 3,837 | — |
| Total input tokens | 6,222,701 | 6,222,701 | — |
| Total output tokens | 682,455 | 682,455 | — |
| Throughput (req/s) | 4.41 | 4.41 | 0.0% |
| Throughput (tok/s) | 784.3 | 784.3 | 0.0% |
| Dropped requests | 0 | 0 | — |
| Timed out requests | 0 | 0 | — |
| Preemption count | 0 | 0 | — |

### End-to-End Latency

| Metric | Baseline | Evolved | Delta |
|--------|----------|---------|-------|
| e2e mean | 11,825 ms | 11,676 ms | **+1.3%** |
| e2e p90 | 23,854 ms | 23,545 ms | **+1.3%** |
| e2e p95 | 32,083 ms | 31,802 ms | **+0.9%** |
| e2e p99 | 58,414 ms | 58,187 ms | **+0.4%** |

### Time to First Token (TTFT)

| Metric | Baseline | Evolved | Delta |
|--------|----------|---------|-------|
| TTFT mean | 1,358 ms | 1,336 ms | **+1.6%** |
| TTFT p90 | 2,934 ms | 2,904 ms | **+1.0%** |
| TTFT p95 | 6,362 ms | 6,361 ms | 0.0% |
| TTFT p99 | 11,455 ms | 11,456 ms | 0.0% |

### Inter-Token Latency (ITL)

| Metric | Baseline | Evolved | Delta |
|--------|----------|---------|-------|
| ITL mean | 58.9 ms | 58.1 ms | **+1.3%** |
| ITL p90 | 43.1 ms | 43.1 ms | 0.0% |
| ITL p95 | 221.5 ms | 98.8 ms | **+55.4%** |
| ITL p99 | 372.9 ms | 372.9 ms | 0.0% |

### Scheduling & KV Cache

| Metric | Baseline | Evolved | Delta |
|--------|----------|---------|-------|
| Scheduling delay p99 | 10,613 ms | 10,531 ms | **+0.8%** |
| Cache hit rate | 47.8% | 49.1% | **+2.7%** |
| Preemption rate | 0.0000 | 0.0000 | — |
| KV thrashing rate | 0.0000 | 0.0000 | — |

### Trajectory Time (top-line metric)

Trajectory time = Σ(turn e2e latencies) per session — the wall-clock time an agent
experiences for a complete multi-turn task. Definition from `shared-experiment-spec.md`.

| Metric | Baseline | Evolved | Delta |
|--------|----------|---------|-------|
| **Mean** | **229,157 ms** | **226,264 ms** | **+1.3%** |
| p50 | 212,987 ms | 207,714 ms | **+2.5%** |
| p90 | 359,553 ms | 356,314 ms | **+0.9%** |
| p99 | 546,439 ms | 542,356 ms | **+0.7%** |

*Positive delta = evolved is better (lower latency).*

---

## Notes

- BLIS is deterministic — one run per configuration, no variance across runs.
- Zero preemptions on both runs: the health scorer's preemption sub-score was always 1.0.
  Improvement comes from rebalanced scorer weights and accurate `active-requests` normalization.
- The reference experiment targets ~13.7% trajectory time improvement. BLIS shows **+1.3%** —
  directionally consistent. Smaller magnitude is expected due to: simulated vs real GPU latency,
  synthesized vs real arrival times, and no KV pressure in this workload.
- Using `queue-depth` instead of `active-requests` produced −2.0% (regression), confirming
  the scorer mapping is load-bearing for correct results.
