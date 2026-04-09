package sim

import "math"

// newHealthScorer creates a stateful 2-signal health scorer.
//
// The scorer combines KV cache pressure and preemption activity to identify
// degraded instances. It is a port of the evolved genome from SkyDiscover
// (updated_health_scorer_program.py):
//
//	composite = 0.55 × kv_score + 0.45 × preemption_score
//
// kv_score: cubic penalty above a utilization threshold.
//
//	= 0                            if KVUtilization ≥ kvThreshold (0.85)
//	= 1 − (KVUtil/kvThreshold)³   otherwise
//
// preemption_score: binary signal based on whether new preemptions occurred
// since the previous routing call on this instance.
//
//	= 0   if PreemptionCount increased since last call
//	= 1   otherwise (instance is stable)
//
// Signal freshness (R17, INV-7):
//
//	Reads: KVUtilization (Periodic/Immediate per ObservabilityConfig) and
//	PreemptionCount (always Immediate — injected unconditionally by
//	CachedSnapshotProvider.Snapshot(), same as InFlightRequests).
//
// State safety: the closure captures prevPre, which is mutated each call.
// Safe because the DES event loop is single-threaded — no concurrent Route() calls.
// This matches the Python reference's default-mutable-arg pattern.
func newHealthScorer() scorerFunc {
	const (
		kvThreshold  = 0.85
		kvWeight     = 0.55
		preWeight    = 0.45
	)

	// prevPre tracks the last-seen PreemptionCount per instance.
	// On first call for an instance, it is initialized to the current count,
	// so delta = 0 and pre_score = 1 (no false penalty on startup).
	// This matches pp.get(w.address, pre) in the Python reference.
	prevPre := make(map[string]int64)

	return func(req *Request, snapshots []RoutingSnapshot) map[string]float64 {
		scores := make(map[string]float64, len(snapshots))

		for _, snap := range snapshots {
			// KV score: cubic penalty above threshold.
			var kvScore float64
			if snap.KVUtilization < kvThreshold {
				ratio := snap.KVUtilization / kvThreshold
				kvScore = 1.0 - math.Pow(ratio, 3)
			}
			// kvScore = 0 when KVUtilization >= kvThreshold (zero-value is correct).

			// Preemption score: 0 if new preemptions occurred, 1 if stable.
			prev, seen := prevPre[snap.ID]
			if !seen {
				prev = snap.PreemptionCount // initialize — delta = 0 on first call
			}
			delta := snap.PreemptionCount - prev
			prevPre[snap.ID] = snap.PreemptionCount

			var preScore float64
			if delta <= 0 {
				preScore = 1.0
			}

			scores[snap.ID] = kvWeight*kvScore + preWeight*preScore
		}

		return scores
	}
}
