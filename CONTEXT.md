# Health Scorer Experiment — Session Context

## What This Is

Reproducing a colleague's (Ophir) OpenShift cluster experiment in BLIS.
Goal: validate BLIS as a faithful proxy for real llm-d routing, and measure
improvement from SkyDiscover-optimized scorer weights.

**Branch:** `feat/health-scorer`
**Worktree:** `/Users/idanfr/Projects/ai-systems/inference-sim/.worktrees/feat-health-scorer`
**Binary:** `/tmp/blis_health` — rebuild with `go build -o /tmp/blis_health main.go`

---

## External Resources

| Resource | Path | What it contains |
|---|---|---|
| SWE-Smith workload (raw) | `/Users/idanfr/Projects/ai-systems/inference-perf/data/swe_smith/` | Original JSON trajectories, not converted. Source for all traces. |
| **Primary comparison (Python llm-d)** | `/Users/idanfr/Projects/ai-systems/llm-d-deploy/results/gateway-baseline-n200/` | Python llm-d baseline — this is what BLIS models. gpu-mem=0.95, Vela cluster. |
| **Primary comparison (Python llm-d)** | `/Users/idanfr/Projects/ai-systems/llm-d-deploy/results/gateway-evolved-n200/` | Python llm-d evolved (Ophir's SkyDiscover genome). Same setup. |
| Secondary (Go EPP, KV pressure) | `/Users/idanfr/Projects/ai-systems/llm-d-deploy/results/go-epp-baseline-v060-n200-gpu070/` | Go EPP baseline, gpu-mem=0.70 (6656 blocks — matches BLIS config) |
| Secondary (Go EPP, KV pressure) | `/Users/idanfr/Projects/ai-systems/llm-d-deploy/results/go-epp-evolved-v060-n200-gpu070/` | Go EPP evolved, same gpu-mem=0.70 setup |
| Go EPP — 1k generalization | `/Users/idanfr/Projects/ai-systems/llm-d-deploy/results/go-epp-*-v060-n200-gpu070-1k/` | Go EPP gpu070, 1000-trajectory pool (tests generalization) |
| Master comparison doc | `/Users/idanfr/Projects/ai-systems/llm-d-deploy/results/COMPARISON.md` | Full cross-system comparison: Python llm-d vs Go EPP vs direct vLLM, all configs |
| Experiment spec | `health-scorer-ref/shared-experiment-spec.md` | Multi-system parity spec. Partially outdated — model in spec is Llama-3.1-8B, real runs use Mistral-Small-24B. Scorer weights section is accurate. |

---

## How to Run

```bash
BINARY=/tmp/blis_health

# Baseline
$BINARY replay \
  --trace-header traces/swe_smith_v3.yaml \
  --trace-data traces/swe_smith_v3.csv \
  --model mistralai/mistral-small-24b-instruct-2501 \
  --num-instances 5 --hardware A100-80 --tp 2 \
  --total-kv-blocks 6656 --block-size-in-tokens 64 \
  --latency-model blackbox \
  --session-mode closed-loop \
  --think-time-mode lognormal \
  --horizon 3600000000 \
  --routing-policy weighted \
  --routing-scorers "prefix-affinity:3.0,kv-utilization:2.0,queue-depth:2.0" \
  --results-path ./runs/baseline_results.json \
  > ./runs/baseline.log

# Evolved (Ophir's SkyDiscover weights)
$BINARY replay \
  --trace-header traces/swe_smith_v3.yaml \
  --trace-data traces/swe_smith_v3.csv \
  --model mistralai/mistral-small-24b-instruct-2501 \
  --num-instances 5 --hardware A100-80 --tp 2 \
  --total-kv-blocks 6656 --block-size-in-tokens 64 \
  --latency-model blackbox \
  --session-mode closed-loop \
  --think-time-mode lognormal \
  --horizon 3600000000 \
  --routing-policy weighted \
  --routing-scorers "precise-prefix-cache:3.5,no-hit-lru:1.0,active-requests:1.5,health:3.2" \
  --results-path ./runs/evolved_results.json \
  > ./runs/evolved.log
```

**Think time modes:**
- `--think-time-mode lognormal` — realistic (~7.4s median), ~48% sessions active at once → low concurrency → lower TTFT
- `--think-time-ms 1` — stress test, all 200 sessions always concurrent → heavy KV pressure → high TTFT

**Exact reproduce commands (all configs):** `runs/benchmark_commands.sh`

---

## Scorer Weights

| Config | Scorers |
|---|---|
| Baseline | `prefix-affinity:3.0, kv-utilization:2.0, queue-depth:2.0` |
| Evolved | `precise-prefix-cache:3.5, no-hit-lru:1.0, active-requests:1.5, health:3.2` |

Evolved weights are Ophir's SkyDiscover-optimized values from the real OpenShift cluster.

---

## Trace Versions

All traces: 200 SWE-Smith trajectories, 3837 turns. Use **v3** for all new runs.

| File | Token estimation | Input tokens (sum) | Prefix sum | Notes |
|---|---|---|---|---|
| `swe_smith` (v1) | uniform char/4 | 2.47M | 41.4M | Original; round-0 prefix_group = session ID (no cross-session KV sharing) |
| `swe_smith_fixed` | uniform char/4 | 2.47M | 41.4M | v1 tokens + prefix_group fix: round-0 uses `swe_smith_system_prompt` so all sessions share system-prompt KV cache |
| `swe_smith_v2` | role-aware (sys=4, user=3, asst=4 ch/tok) | 3.29M | 51.7M | Better token estimates + prefix_group fix |
| `swe_smith_v3` | exact Mistral-Small-24B HF tokenizer | 3.41M | 52.8M | Ground truth; v2 and v3 agree within 2% |

**Use:** `--trace-header traces/swe_smith_v3.yaml --trace-data traces/swe_smith_v3.csv`

Regenerate v3: `python3 scripts/tokenize_swe_smith.py --input /Users/idanfr/Projects/ai-systems/inference-perf/data/swe_smith/swe_smith_workload.json --output traces/swe_smith_v3 --turn-gap-us 100000`

---

## Run Inventory (`runs/`)

Canonical results only. All obsolete/broken runs deleted.

| File | Think time | Trace | TTFT mean | Cache hit | Preemptions | Completed |
|---|---|---|---|---|---|---|
| `lognormal_v2_baseline.log` | lognormal | v2 | 5522ms | 67.5% | 34 | 3826 |
| `lognormal_v2_evolved.log` | lognormal | v2 | 4574ms | 77.3% | 6 | 3837 |
| `fixed_baseline_v2.log` | lognormal | v2 | 2582ms | 79.2% | 0 | 3837 |
| `fixed_evolved_v2.log` | lognormal | v2 | 1836ms | 84.1% | 0 | 3837 |
| `v3_baseline.log` | 1ms | v3 | 7489ms | 68.0% | 82 | 3646 |
| `v3_evolved.log` | 1ms | v3 | 6749ms | 68.6% | 106 | 3720 |
| `v3_evolved_oracle.log` | 1ms, cache-delay=0 | v3 | 6749ms | 68.6% | 106 | 3720 |
| `fixed_v3_baseline.log` | lognormal | v3 | 5938ms | 62.9% | 64 | 3670 |
| `fixed_v3_evolved.log` | lognormal | v3 | 6153ms | 73.0% | 19 | 3837 |
| `zero_think_baseline.log` | ? (unknown config) | ? | 1183ms | 91.6% | 0 | 3837 |
| `zero_think_evolved.log` | ? (unknown config) | ? | 657ms | 92.3% | 0 | 3837 |

`runs/benchmark_commands.sh` — most recent exact commands used.

---

## Comparison: Real Cluster vs BLIS

### Real Cluster — Go EPP gpu070 (median of 5 runs)

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| Trajectory time p50 | 484.1s | 387.4s | **−20.0%** |
| Trajectory time mean | 442.5s | 366.0s | **−17.3%** |
| Output tok/s | 677.1 | 765.1 | **+13.0%** |
| Prefix cache hit rate | ~57% | 72–95% | **+15–38pp** |
| Preemptions (5 runs total) | ~320 | ~46 | **−86%** |
| Completed per run | 177/200 | 177/200 | — |

### BLIS — Lognormal think time, v2 trace

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| TTFT mean | 5522ms | 4574ms | **−17.2%** |
| TTFT p90 | 14027ms | 12004ms | **−14.4%** |
| Prefix cache hit rate | 67.5% | 77.3% | **+9.8pp** |
| Preemptions | 34 | 6 | **−82%** |
| Completed | 3826 | 3837 | **+0.3%** |
| Timed out | 1 | 0 | — |

### BLIS — Lognormal think time, v3 trace

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| TTFT mean | 5938ms | 6153ms | **+3.6% (worse)** |
| TTFT p90 | 14761ms | 19630ms | **+33% (worse)** |
| Prefix cache hit rate | 62.9% | 73.0% | **+10.1pp** |
| Preemptions | 64 | 19 | **−70%** |
| Completed | 3670 | 3837 | **+4.5%** |
| Timed out | 13 | 0 | **−100%** |

**v2 results align well with real cluster (TTFT −17% vs real −20%, cache hit and preemptions correct direction).
v3 TTFT regresses — the ~3.5% more tokens in v3 pushes closer to KV capacity edge, exposing the health scorer weight mismatch.
Cache hit and preemptions agree with real cluster in both v2 and v3.**

---

## Current Best Results (lognormal think time, v3 trace, 2026-04-13)

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| Completed | 3670 | 3837 | +4.5% |
| Timed out | 13 | 0 | −100% |
| TTFT mean | 5938ms | 6153ms | +3.6% (worse) |
| TTFT p90 | 14761ms | 19630ms | +33% (worse) |
| Cache hit rate | 62.9% | 73.0% | +10.1pp |
| Preemptions | 64 | 19 | −70% |

---

## Real Cluster Reference (median of 5 runs)

### Primary: Python llm-d (what BLIS models) — gpu-mem=0.95, Vela cluster

Source: `llm-d-deploy/results/gateway-{baseline,evolved}-n200/`

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| Trajectory time p50 | 169.1s | 142.5s | −15.7% |
| Trajectory time mean | 177.2s | 153.3s | −13.5% |
| Overall TTFT p50 | 0.327s | 0.303s | −7.3% |
| Overall TTFT mean | 1.112s | 0.583s | −47.6% |
| Output tok/s | 1039.0 | 1059.0 | +1.9% |
| Completed | 3636/3837 | 3636/3837 | same |

Note: gpu-mem=0.95 → minimal KV pressure → no preemptions. Health scorer has limited signal here.

### Secondary: Go EPP — gpu-mem=0.70 (KV pressure, matches BLIS block count)

Source: `llm-d-deploy/results/go-epp-{baseline,evolved}-v060-n200-gpu070/`
Same hardware as BLIS config: 5 workers, TP=2, A100 80GB×2, 6656 KV blocks.

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| Trajectory time p50 | 484.1s | 387.4s | −20.0% |
| Trajectory time mean | 442.5s | 366.0s | −17.3% |
| Output tok/s | 677.1 | 765.1 | +13.0% |
| Preemptions (total, 5 runs) | ~320 | ~46 | −86% |
| Prefix cache hit rate | ~57% | 72–95% | much better |

**Evolved scorer detail** (from `epp-configmap.yaml`):
- `precise-prefix-cache: 3.5`, `no-hit-lru: 1.0`, `active-request: 1.5`
- `health: 3.2` (kvCacheThreshold=0.85, kvWeight=0.55, preemptionWeight=0.45)

---

## Uncommitted Code Changes

Three files modified, not yet committed:

| File | Change |
|---|---|
| `sim/workload/session.go` | Fix double-accumulation bug: `contextTokens` was appended with full `req.InputTokens` (which already contained `contextTokens`), causing ~2× context growth per round. Fix: only append new suffix `req.InputTokens[len(sess.contextTokens):]` |
| `sim/workload/session_test.go` | Updated `TestSession_ContextAccumulation_MultiStep` to match correct (non-doubled) token counts |
| `sim/workload/convert_swe_smith.go` | Role-aware char/token constants: system=4, user=3, assistant=4 chars/tok (vs uniform char/4 before) |

Tests pass: `go test ./sim/workload/...`

---

## Open Questions

1. **TTFT discrepancy**: lognormal+v2 runs (`fixed_*_v2.log`) show 2582ms/1836ms with 0 preemptions, but lognormal+v3 runs show 5938ms/6153ms with 64/19 preemptions. Why does v3 (more accurate token counts) produce worse TTFT and introduces preemptions? Suspect: v3 has larger tokens → more KV blocks per request → more pressure even at lognormal concurrency.

2. **zero_think runs**: `zero_think_baseline.log` shows TTFT 1183ms with 0 preemptions and 3837 completions. Config unknown — what trace and think time was used? These are suspiciously clean results.

3. **Evolved TTFT worse than baseline (lognormal+v3)**: Evolved has better cache hit (+10pp) and fewer preemptions (−70%) but slightly worse mean TTFT. The health:3.2 weight may be routing too aggressively, causing some instances to be underloaded while others queue.

4. **BLIS vs real cluster preemption model gap**: BLIS only evicts from batch being formed; vLLM can interrupt any running request. Limits preemption count fidelity.

---

## Next Session: Start Here

1. **Understand v2 vs v3 TTFT gap** — run lognormal+v3 with verbose per-instance stats to see if pressure is unevenly distributed. Compare with fixed_baseline_v2 run flags.
2. **Identify zero_think config** — check `runs/benchmark_commands.sh` history or re-run to reproduce.
3. **Commit the three modified files** once results make sense.

---

## Change Log

| Date | What | Outcome |
|---|---|---|
| 2026-04-12 | Created `scripts/tokenize_swe_smith.py` for exact Mistral tokenization | v3 trace: 3837 turns, 56.2M tokens |
| 2026-04-12 | Fixed double-accumulation bug in `session.go` | Context growth now correct; see TTFT discrepancy open question |
| 2026-04-12 | Role-aware char/tok constants in `convert_swe_smith.go` | v2 trace within 2% of v3 |
| 2026-04-13 | Re-ran baseline + evolved with v3 trace, lognormal think time | TTFT 5938ms/6153ms — higher than v2 runs, reason under investigation |
| 2026-04-13 | Cleaned up `tmp/` → `runs/`, deleted 14 obsolete/broken log files | Canonical runs only retained |
| 2026-04-13 | Ran lognormal baseline + evolved with v2 trace | TTFT −17.2%, cache +9.8pp, preemptions −82% — good real-cluster alignment |
