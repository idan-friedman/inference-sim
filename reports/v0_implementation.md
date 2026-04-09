# BLIS Implementation Report — Health Scorer & Parity Experiment

## Overview

Changes made to BLIS to support the cross-system parity experiment comparing routing algorithm
improvements across Python llm-d, Go EPP, and BLIS.

---

## Part 1 — Health Scorer

A new `health` routing scorer was implemented, porting the evolved genome from
`updated_health_scorer_program.py`.

### Formula

```
composite = 0.55 × kv_score + 0.45 × preemption_score
```

| Signal | Formula |
|--------|---------|
| `kv_score` | `1 − (KVUtil / 0.85)³` if KVUtil < 0.85, else `0` |
| `preemption_score` | `0` if preemption count increased since last routing call, else `1` |

### New signal: PreemptionCount

`sim.Metrics.PreemptionCount` existed inside the simulator but was not exposed to the router.
It was wired through the stack:

- `PreemptionCount int64` added to `RoutingSnapshot` (router data contract)
- `PreemptionCount()` accessor added to `InstanceSimulator`
- Wired unconditionally in `CachedSnapshotProvider` — always fresh, unaffected by `--snapshot-refresh-interval`

### Files

| File | Change |
|------|--------|
| `sim/routing.go` | Added `PreemptionCount int64` to `RoutingSnapshot` |
| `sim/cluster/instance.go` | Added `PreemptionCount()` accessor |
| `sim/cluster/snapshot.go` | Wired in `Snapshot()` and `RefreshAll()` |
| `sim/routing_health_scorer.go` | New — stateful scorer closure |
| `sim/routing_scorers.go` | Registered `"health"` scorer |
| `sim/routing_health_scorer_test.go` | New — 8 behavioral tests |
| `sim/cluster/snapshot_test.go` | Extended — PreemptionCount freshness test |

---

## Part 2 — Active-Requests Scorer

The evolved config references `active_request` from llm-d. Inspecting the
[llm-d source](https://github.com/llm-d/llm-d-inference-scheduler/blob/ea4610da4965b6cb5c32a12032d8598c741f9e36/pkg/plugins/scorer/active_request.go)
revealed a different normalization than BLIS's existing `queue-depth`:

| Scorer | Signal | Normalization |
|--------|--------|---------------|
| `queue-depth` | `QueueDepth + BatchSize + InFlightRequests` | `(max − load) / (max − min)` |
| `active-requests` *(new)* | `InFlightRequests` only | `(max − count) / max` — anchored at zero |

Using `queue-depth` as a proxy produced a −2.0% regression. Implementing `active-requests`
correctly flipped the result to +1.3% improvement.

| File | Change |
|------|--------|
| `sim/routing_scorers.go` | Added `scoreActiveRequests` and registered `"active-requests"` |

---

## Part 3 — Workload Converters

### `blis convert swe-smith`

Converts `swe_smith_workload.json` (SWE-agent coding trajectories) to TraceV2.
Token counts estimated from character length (chars/4). Arrival times synthesized
via `--turn-gap-ms` (default 30s).

| File | Change |
|------|--------|
| `sim/workload/convert_swe_smith.go` | New converter library |
| `cmd/convert_swe_smith.go` | New CLI command |

### `blis convert chat-trace`

Converts multi-turn chat production JSONL (Qwen `blksz_16` format) to TraceV2.
Prefix lengths derived from `hash_ids` block overlap between consecutive turns.

| File | Change |
|------|--------|
| `sim/workload/convert_chat_trace.go` | New converter library (renamed from `convert_qwen.go`) |
| `cmd/convert_chat_trace.go` | New CLI command (renamed from `convert_qwen.go`) |

---

## Test Status

```
ok  github.com/inference-sim/inference-sim/sim
ok  github.com/inference-sim/inference-sim/sim/cluster
ok  github.com/inference-sim/inference-sim/sim/workload
ok  github.com/inference-sim/inference-sim/cmd
```
