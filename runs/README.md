# BLIS Routing Comparison — SWE-Smith / Mistral-Small-24B

## Purpose

Reproduce a colleague's OpenShift cluster experiment in BLIS by comparing two routing configurations (baseline vs evolved) on the SWE-Smith workload. Goal: validate that BLIS is a faithful proxy for real llm-d routing behavior and measure the improvement from Ophir's SkyDiscover-optimized scorer weights.

Part of a multi-system parity comparison (`health-scorer-ref/shared-experiment-spec.md`):

| System | Owner | Role |
|---|---|---|
| Python llm-d | Yevgeny / Ophir | baseline + evolved |
| Go EPP | Yevgeny | baseline only |
| BLIS | Inbar / Idan | baseline + evolved — this experiment |

## Setup

**Branch:** `feat/health-scorer`
**Worktree:** `/Users/idanfr/Projects/ai-systems/inference-sim/.worktrees/feat-health-scorer`
**Binary:** `/tmp/blis_health` — built from the worktree (`go build -o /tmp/blis_health main.go`)

### Configuration

| Parameter | Value | Notes |
|---|---|---|
| Model | `mistralai/mistral-small-24b-instruct-2501` | Closest to real `openai/gpt-oss-20b` in defaults.yaml |
| Hardware | 5x A100-80, TP=2 | Matches real OpenShift cluster |
| KV blocks | 6656, block_size=64 | 0.70×80GB − 24GB(weights) ≈ 32GB = ~6656 blocks |
| Latency model | blackbox | Uses A100-80 + TP=2 coefficients from defaults.yaml |
| Session mode | closed-loop | Follow-ups fire at completion_time + think_time |
| Horizon | 3600000000 µs (1 hour) | All r0 arrivals at t=0; default 600s truncates sessions |

### Scorer Weights

**Baseline** (from `shared-experiment-spec.md`):
```
prefix-affinity:3.0, kv-utilization:2.0, queue-depth:2.0
```

**Evolved** (Ophir's SkyDiscover optimization — real cluster weights):
```
precise-prefix-cache:3.5, no-hit-lru:1.0, active-requests:1.5, health:3.2
```

---

## Trace Versions

Three trace generations from the same 200-trajectory SWE-Smith workload:

| Version | Token estimation | System prompt | User/Asst | Total prompt tokens |
|---|---|---|---|---|
| `swe_smith.csv` (v1) | char/4 uniform | 4 ch/tok | 4 ch/tok | ~43M |
| `swe_smith_v2.csv` | role-aware char/tok | 4 ch/tok | 3/4 ch/tok | 55.0M |
| `swe_smith_v3.csv` | exact Mistral tokenizer | exact | exact | 56.2M |

**v3 generation:** `python3 scripts/tokenize_swe_smith.py --input data/swe_smith_workload.json --output traces/swe_smith_v3 --turn-gap-us 100000`

v2 and v3 agree within 2% on total prompt tokens (55.0M vs 56.2M), confirming the role-aware char/token constants were already very accurate.

---

## Experiment Results

### Think-time: lognormal (µ=2.0, σ=0.6, clamp [3s, 30s]) — realistic pacing

Session pacing is realistic: ~48% of sessions active at any point. Zero KV cache pressure (peak ~2300 blocks/instance vs 6656 capacity). No preemptions.

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| Completed | 3837 | 3837 | 0% |
| TTFT mean | 1475ms | 732ms | **−50%** |
| Cache hit rate | 88.7% | 92.0% | **+3.3pp** |
| Preemptions | 0 | 0 | — |

(Files: `fixed_v3_baseline.log`, `fixed_v3_evolved.log`)

### Think-time: 1ms (near-zero) — maximum KV cache pressure

All 200 sessions compete simultaneously. KV cache pressure is severe; preemptions appear.

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| Completed | 3646 | 3720 | +2.0% |
| Timed out | 16 | 9 | −44% |
| Preemptions | 82 | 106 | +29% |
| TTFT mean | 7489ms | 6749ms | **−10%** |
| Cache hit rate | 67.98% | 68.62% | +0.6pp |

(Files: `v3_baseline.log`, `v3_evolved.log`)

The evolved weights improve TTFT by 10% and reduce timeouts by 44%, but show more preemptions. The weights were optimized against the real cluster (different dynamics than BLIS's blackbox latency model); this weight-model mismatch explains the preemption regression. TTFT improvement is real and consistent.

---

## Real Cluster Reference (from `llm-d-deploy/results/go-epp-evolved-v060-n200-gpu070/`)

| Metric | Baseline | Evolved | Delta |
|---|---|---|---|
| Trajectory time p50 | 489s | 387s | **−20.8%** |
| Trajectory time mean | 450s | 366s | **−18.7%** |
| Preemptions (total) | ~171 | ~46 | **−73%** |
| Prefix cache hit rate | 57% | 72–95% | much better |

---

## Key Fixes Applied

1. **Double-accumulation bug** (`session.go`): `contextTokens` was appended with `req.InputTokens` (which already contained `contextTokens`), causing ~2× context growth per round. Fixed by appending only the new suffix: `req.InputTokens[len(sess.contextTokens):]`.

2. **Token estimation** (`convert_swe_smith.go`): Changed from uniform char/4 to role-aware constants (system=4, user=3, assistant=4 chars/token) derived from Mistral-Small-24B tokenizer measurements on 20 trajectories. Error reduced from ~26% to <5%.

3. **Exact tokenization** (`scripts/tokenize_swe_smith.py`): Python script using the actual Mistral-Small-24B HF tokenizer. Produces `swe_smith_v3.csv` with zero estimation error. v3 is within 2% of v2 totals — confirming the role-aware constants were sufficient.

4. **Prefix group sharing** (`convert_swe_smith.go`): Round-0 records use `swe_smith_system_prompt` as prefix group so all sessions share the same synthetic system-prompt token sequence, correctly modeling cross-session KV cache hits on the system prompt.

---

## Output Files

| File | Contents |
|---|---|
| `fixed_v3_baseline.log` | Lognormal think time, baseline routing |
| `fixed_v3_evolved.log` | Lognormal think time, evolved routing |
| `v2_baseline.log` | 1ms think time, baseline, v2 trace |
| `v2_evolved.log` | 1ms think time, evolved, v2 trace |
| `v3_baseline.log` | 1ms think time, baseline, v3 trace (exact tok) |
| `v3_evolved.log` | 1ms think time, evolved, v3 trace (exact tok) |
| `v3_evolved_oracle.log` | 1ms think time, evolved, v3, cache-signal-delay=0 |
| `benchmark_commands.sh` | Exact commands to reproduce runs |
