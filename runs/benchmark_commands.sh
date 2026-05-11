#!/usr/bin/env bash
# Reproduce commands for health-scorer baseline vs evolved comparison.
# Run from: /Users/idanfr/Projects/ai-systems/inference-sim/.worktrees/feat-health-scorer
# Results documented in CONTEXT.md.

BINARY=/tmp/blis_health
COMMON="--model mistralai/mistral-small-24b-instruct-2501 --num-instances 5 --hardware A100-80 --tp 2 --total-kv-blocks 6656 --block-size-in-tokens 64 --latency-model blackbox --session-mode closed-loop --think-time-mode lognormal --horizon 3600000000 --routing-policy weighted"

# Build (if needed):
# go build -o /tmp/blis_health main.go

# ── v2 trace (canonical, best real-cluster alignment) ─────────────────────────

$BINARY replay \
  --trace-header traces/swe_smith_v2.yaml \
  --trace-data traces/swe_smith_v2.csv \
  $COMMON \
  --routing-scorers "prefix-affinity:3.0,kv-utilization:2.0,queue-depth:2.0" \
  --results-path ./runs/lognormal_v2_baseline_results.json \
  > ./runs/lognormal_v2_baseline.log

$BINARY replay \
  --trace-header traces/swe_smith_v2.yaml \
  --trace-data traces/swe_smith_v2.csv \
  $COMMON \
  --routing-scorers "precise-prefix-cache:3.5,no-hit-lru:1.0,active-requests:1.5,health:3.2" \
  --results-path ./runs/lognormal_v2_evolved_results.json \
  > ./runs/lognormal_v2_evolved.log

# ── v3 trace (exact tokenizer, more KV pressure — see CONTEXT.md) ─────────────

$BINARY replay \
  --trace-header traces/swe_smith_v3.yaml \
  --trace-data traces/swe_smith_v3.csv \
  $COMMON \
  --routing-scorers "prefix-affinity:3.0,kv-utilization:2.0,queue-depth:2.0" \
  --results-path ./runs/fixed_v3_baseline_results.json \
  > ./runs/fixed_v3_baseline.log

$BINARY replay \
  --trace-header traces/swe_smith_v3.yaml \
  --trace-data traces/swe_smith_v3.csv \
  $COMMON \
  --routing-scorers "precise-prefix-cache:3.5,no-hit-lru:1.0,active-requests:1.5,health:3.2" \
  --results-path ./runs/fixed_v3_evolved_results.json \
  > ./runs/fixed_v3_evolved.log

# ── 1ms stress test (all 200 sessions concurrent, v3 trace) ───────────────────

$BINARY replay \
  --trace-header traces/swe_smith_v3.yaml \
  --trace-data traces/swe_smith_v3.csv \
  --model mistralai/mistral-small-24b-instruct-2501 --num-instances 5 --hardware A100-80 --tp 2 \
  --total-kv-blocks 6656 --block-size-in-tokens 64 --latency-model blackbox \
  --session-mode closed-loop --think-time-ms 1 --horizon 3600000000 --routing-policy weighted \
  --routing-scorers "prefix-affinity:3.0,kv-utilization:2.0,queue-depth:2.0" \
  --results-path ./runs/v3_baseline_results.json \
  > ./runs/v3_baseline.log

$BINARY replay \
  --trace-header traces/swe_smith_v3.yaml \
  --trace-data traces/swe_smith_v3.csv \
  --model mistralai/mistral-small-24b-instruct-2501 --num-instances 5 --hardware A100-80 --tp 2 \
  --total-kv-blocks 6656 --block-size-in-tokens 64 --latency-model blackbox \
  --session-mode closed-loop --think-time-ms 1 --horizon 3600000000 --routing-policy weighted \
  --routing-scorers "precise-prefix-cache:3.5,no-hit-lru:1.0,active-requests:1.5,health:3.2" \
  --results-path ./runs/v3_evolved_results.json \
  > ./runs/v3_evolved.log
