# Health Scorer — Implementation Plan

## Context

Cross-system parity experiment comparing routing algorithm improvements across Python llm-d, Go EPP,
and BLIS. Each system runs baseline vs evolved scoring; if all show ~13.7% improvement in
trajectory time, the systems are faithful proxies of each other.

BLIS runs both configurations:
- **Baseline**: `prefix-affinity:3, kv-utilization:2, queue-depth:2`
- **Evolved**: health scorer (2-signal) + boosted prefix-affinity + queue-depth

Top-line metric: **trajectory time** = Σ(turn latencies) per session.

Reference: `shared-experiment-spec.md`

---

## Part 1 — Scorer Implementation

### Scorer: 2-signal health scorer (from `updated_health_scorer_program.py`)

```
composite = 0.55 × kv_score + 0.45 × preemption_score
```

| Sub-score | Formula | Signal |
|---|---|---|
| `kv_score` | `0` if `KVUtil ≥ 0.85`, else `1 − (KVUtil/0.85)³` | `RoutingSnapshot.KVUtilization` (already exists) |
| `preemption_score` | `0` if count increased since last call, else `1` | `RoutingSnapshot.PreemptionCount` (need to add) |

Only `PreemptionCount` needs to be wired — no TTFT infrastructure required.
`PreemptionCount` already lives in `sim.Metrics.PreemptionCount` per instance;
it just isn't surfaced to the router yet.

### Files to modify

| File | Change |
|---|---|
| `sim/routing.go` | Add `PreemptionCount int64` to `RoutingSnapshot` struct |
| `sim/cluster/instance.go` | Add `PreemptionCount() int64` accessor method |
| `sim/cluster/snapshot.go` | Wire `PreemptionCount` unconditionally in `Snapshot()` and `RefreshAll()` |
| `sim/routing_scorers.go` | Add `"health": true` to `validScorerNames`; add case to factory |
| `sim/routing_health_scorer.go` | New file: scorer factory + stateful closure |

### T1 — `sim/routing.go`: Add field to `RoutingSnapshot`

```go
PreemptionCount int64  // cumulative preemptions since instance start (monotonically increasing)
```

`RoutingSnapshot` is the data contract between cluster and router (INV-9). Adding a field here
officially exposes the signal to all scoring policies. It's a monotonically increasing counter;
the scorer computes deltas internally. No existing callers break — struct literal construction
happens in exactly one place (`NewRoutingSnapshot`).

### T2 — `sim/cluster/instance.go`: Add accessor method

Follow the exact pattern of existing accessors (`KVUtilization`, `QueueDepth`):

```go
func (i *InstanceSimulator) PreemptionCount() int64 { return i.sim.Metrics.PreemptionCount }
```

This keeps `CachedSnapshotProvider` decoupled from `sim.Metrics` internals — snapshot tests
can mock the instance interface without a full simulator.

### T3 — `sim/cluster/snapshot.go`: Wire the field

Read unconditionally (no staleness gating) — same approach as `InFlightRequests`. The
preemption delta check (`pre_score = 0 if delta > 0`) requires a fresh value; stale reads
could miss events between periodic refreshes.

Add to both `Snapshot()` and `RefreshAll()`:

```go
snap.PreemptionCount = inst.PreemptionCount()
```

### T4 — `sim/routing_health_scorer.go`: Implement scorer (new file)

Pattern: follow `newPrefixAffinityScorer` in `routing_prefix_scorer.go`.
Returns `(scorerFunc, nil)` — no observer needed (no post-routing state to update).

Closure state:
```go
prevPre map[string]int64  // per-instance last-seen PreemptionCount
```

Per-call logic:
1. `kv_score = 0` if `snap.KVUtilization ≥ 0.85`, else `1 − (snap.KVUtilization / 0.85)³`
2. `delta = snap.PreemptionCount − prevPre[id]`; update `prevPre[id]`
3. `pre_score = 0` if `delta > 0`, else `1`
4. Return `0.55 × kv_score + 0.45 × pre_score`

Note on first call: `prevPre` starts empty. On first call for a given instance,
`prevPre[id]` is initialized to the current count → `delta = 0` → `pre_score = 1`.
Matches the Python reference (`pp.get(w.address, pre)` defaults to `pre`).

### T5 — `sim/routing_scorers.go`: Register scorer

```go
// validScorerNames:
"health": true,

// newScorerWithObserver switch:
case "health":
    return newHealthScorer(), nil
```

### T6 — Tests

**`sim/routing_health_scorer_test.go`** (new):

| Scenario | Assertion |
|---|---|
| First call, any KV | pre_score = 1.0 (delta = 0 on init) |
| KV = 0.0 | kv_score = 1.0 |
| KV = 0.5 | kv_score ≈ 0.796 (`1 − (0.5/0.85)³`) |
| KV = 0.85 (at threshold) | kv_score = 0.0 |
| KV = 1.0 (above threshold) | kv_score = 0.0 |
| PreemptionCount stable across calls | pre_score = 1.0 |
| PreemptionCount increases on call N | pre_score = 0.0 on call N |
| PreemptionCount stable on call N+1 | pre_score = 1.0 again |
| All scores ∈ [0, 1] | invariant for any input |
| Single instance | no panics, correct composite |

**`sim/routing_scorers_test.go`** (extend):
- `"health"` accepted by `ParseScorerConfigs`; `IsValidScorer("health")` = true

**`sim/cluster/snapshot_test.go`** (extend):
- `PreemptionCount` populated in snapshot; unaffected by `SnapshotRefreshInterval`

---

## Part 2 — Workload: Qwen Trace Converter

### T7 — Convert `qwen_traceA_blksz_16.jsonl` → BLIS TraceV2

BLIS has no existing converter for the Alibaba Qwen production trace format.
Implement `blis convert qwen-trace --input FILE --output PREFIX` following the
existing converter pattern in `cmd/convert.go`.

Output: BLIS TraceV2 (`.yaml` header + `.csv` data).

---

## Part 3 — Experiment Config & Metric

### Infrastructure (from spec)

```
workers (instances): 5
model:               meta-llama/Llama-3.1-8B-Instruct
gpu:                 A100 SXM4 80GB, 1 per worker
block_size:          64  (--block-size-in-tokens 64)
```

### Scorer config mapping

| Reference scorer | BLIS | Note |
|---|---|---|
| `precise_prefix_cache` | `prefix-affinity` | Direct |
| `kv_cache_utilization` | `kv-utilization` | Direct |
| `queue` / `active_request` | `queue-depth` | Same signal |
| `health_scorer` | `health` | New (Part 1) |
| `no_hit_lru` | *(dropped)* | No equivalent; ~5% weight |

**Baseline:** `--routing-scorers prefix-affinity:3,kv-utilization:2,queue-depth:2`

**Evolved:** `--routing-scorers prefix-affinity:4.0,queue-depth:3.8,health:3.5`

### Run commands

```bash
# Baseline
./blis replay \
  --trace-header qwen_trace.yaml --trace-data qwen_trace.csv \
  --model meta-llama/Llama-3.1-8B-Instruct \
  --num-instances 5 --block-size-in-tokens 64 \
  --routing-policy weighted \
  --routing-scorers prefix-affinity:3,kv-utilization:2,queue-depth:2 \
  --output baseline_results.json

# Evolved
./blis replay \
  --trace-header qwen_trace.yaml --trace-data qwen_trace.csv \
  --model meta-llama/Llama-3.1-8B-Instruct \
  --num-instances 5 --block-size-in-tokens 64 \
  --routing-policy weighted \
  --routing-scorers prefix-affinity:4.0,queue-depth:3.8,health:3.5 \
  --output evolved_results.json
```

BLIS is deterministic (INV-6) — one run per configuration suffices.

---

## Verification

```bash
go build ./...
go test ./sim/... ./sim/cluster/...

# Smoke-run
./blis run --model meta-llama/Llama-3.1-8B-Instruct \
  --num-instances 5 --routing-policy weighted \
  --routing-scorers prefix-affinity:4.0,queue-depth:3.8,health:3.5
```
