package sim

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHealthScorer_KVScore_Formula verifies the cubic penalty formula at key boundary values.
//
// kv_score = 0                          if KVUtilization >= 0.85
//
//	= 1 - (KVUtilization/0.85)^3   otherwise
func TestHealthScorer_KVScore_Formula(t *testing.T) {
	tests := []struct {
		name        string
		kvUtil      float64
		wantKVScore float64
	}{
		{"zero utilization", 0.0, 1.0},
		{"half utilization", 0.5, 1.0 - math.Pow(0.5/0.85, 3)},
		{"just below threshold", 0.849, 1.0 - math.Pow(0.849/0.85, 3)},
		{"at threshold", 0.85, 0.0},
		{"above threshold", 0.95, 0.0},
		{"full utilization", 1.0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := newHealthScorer()
			snaps := []RoutingSnapshot{
				{ID: "inst_0", KVUtilization: tt.kvUtil, PreemptionCount: 0},
			}
			scores := scorer(nil, snaps)

			// With pre_score = 1 (no preemption on first call), composite = 0.55*kv + 0.45*1
			wantComposite := 0.55*tt.wantKVScore + 0.45*1.0
			assert.InDelta(t, wantComposite, scores["inst_0"], 1e-9,
				"composite score mismatch for KVUtil=%.3f", tt.kvUtil)
		})
	}
}

// TestHealthScorer_PreemptionScore_FirstCall verifies that the first call for any
// instance initializes prevPre to the current count, producing pre_score = 1.0
// (no false penalty on startup).
func TestHealthScorer_PreemptionScore_FirstCall(t *testing.T) {
	scorer := newHealthScorer()
	snaps := []RoutingSnapshot{
		{ID: "inst_0", KVUtilization: 0.0, PreemptionCount: 42},
	}
	scores := scorer(nil, snaps)

	// pre_score = 1.0 on first call (delta = 0 after initialization)
	// kv_score = 1.0 (KVUtil = 0)
	assert.InDelta(t, 1.0, scores["inst_0"], 1e-9,
		"first call must not penalize for existing preemption count")
}

// TestHealthScorer_PreemptionScore_DeltaDetection verifies the stateful delta logic:
// - score = 1.0 when count is stable
// - score = 0.0 when count increases
// - score = 1.0 again when count is stable on the next call
func TestHealthScorer_PreemptionScore_DeltaDetection(t *testing.T) {
	scorer := newHealthScorer()
	snap := func(preCount int64) []RoutingSnapshot {
		return []RoutingSnapshot{{ID: "inst_0", KVUtilization: 0.0, PreemptionCount: preCount}}
	}

	// Call 1: initialize (pre_score = 1, delta = 0)
	scorer(nil, snap(10))

	// Call 2: count stable → pre_score = 1.0
	scores2 := scorer(nil, snap(10))
	assert.InDelta(t, 1.0, scores2["inst_0"], 1e-9,
		"stable preemption count → pre_score = 1.0")

	// Call 3: count increases → pre_score = 0.0
	scores3 := scorer(nil, snap(15))
	wantKV := 1.0 // KVUtil = 0
	wantComposite := 0.55*wantKV + 0.45*0.0
	assert.InDelta(t, wantComposite, scores3["inst_0"], 1e-9,
		"increasing preemption count → pre_score = 0.0")

	// Call 4: count stable again → pre_score = 1.0
	scores4 := scorer(nil, snap(15))
	assert.InDelta(t, 1.0, scores4["inst_0"], 1e-9,
		"preemption count stable again → pre_score = 1.0")
}

// TestHealthScorer_Invariant_ScoresInRange verifies that all returned scores are in [0, 1]
// for a wide range of KV utilizations and preemption states.
func TestHealthScorer_Invariant_ScoresInRange(t *testing.T) {
	scorer := newHealthScorer()

	kvValues := []float64{0.0, 0.1, 0.5, 0.84, 0.85, 0.9, 1.0}
	preValues := []int64{0, 1, 5, 100}

	for _, kv := range kvValues {
		for _, pre := range preValues {
			snaps := []RoutingSnapshot{
				{ID: "inst_0", KVUtilization: kv, PreemptionCount: pre},
				{ID: "inst_1", KVUtilization: 0.5, PreemptionCount: pre + 1},
			}
			scores := scorer(nil, snaps)
			for id, score := range scores {
				assert.GreaterOrEqual(t, score, 0.0, "score for %s below 0 (kv=%.2f, pre=%d)", id, kv, pre)
				assert.LessOrEqual(t, score, 1.0, "score for %s above 1 (kv=%.2f, pre=%d)", id, kv, pre)
				assert.False(t, math.IsNaN(score), "NaN score for %s", id)
				assert.False(t, math.IsInf(score, 0), "Inf score for %s", id)
			}
		}
	}
}

// TestHealthScorer_SingleInstance_NoPanic verifies no division-by-zero or panic
// when only one instance is present.
func TestHealthScorer_SingleInstance_NoPanic(t *testing.T) {
	scorer := newHealthScorer()
	snaps := []RoutingSnapshot{
		{ID: "only", KVUtilization: 0.5, PreemptionCount: 0},
	}
	assert.NotPanics(t, func() {
		scores := scorer(nil, snaps)
		assert.Contains(t, scores, "only")
	})
}

// TestHealthScorer_MultipleInstances_IndependentState verifies that each instance's
// preemption delta is tracked independently — a preemption on inst_0 does not affect
// the score of inst_1.
func TestHealthScorer_MultipleInstances_IndependentState(t *testing.T) {
	scorer := newHealthScorer()

	// Call 1: initialize both instances
	scorer(nil, []RoutingSnapshot{
		{ID: "inst_0", KVUtilization: 0.0, PreemptionCount: 10},
		{ID: "inst_1", KVUtilization: 0.0, PreemptionCount: 5},
	})

	// Call 2: inst_0 has new preemptions, inst_1 is stable
	scores := scorer(nil, []RoutingSnapshot{
		{ID: "inst_0", KVUtilization: 0.0, PreemptionCount: 20}, // delta = 10 → pre_score = 0
		{ID: "inst_1", KVUtilization: 0.0, PreemptionCount: 5},  // delta = 0  → pre_score = 1
	})

	wantInst0 := 0.55*1.0 + 0.45*0.0 // kv=1, pre=0
	wantInst1 := 0.55*1.0 + 0.45*1.0 // kv=1, pre=1

	assert.InDelta(t, wantInst0, scores["inst_0"], 1e-9,
		"inst_0 should be penalized for new preemptions")
	assert.InDelta(t, wantInst1, scores["inst_1"], 1e-9,
		"inst_1 should not be affected by inst_0's preemptions")
}

// TestHealthScorer_IsValidAndRegistered verifies "health" is a known scorer name
// and is accepted by ParseScorerConfigs.
func TestHealthScorer_IsValidAndRegistered(t *testing.T) {
	assert.True(t, IsValidScorer("health"), "health must be a valid scorer name")

	configs, err := ParseScorerConfigs("health:3.5")
	assert.NoError(t, err)
	assert.Len(t, configs, 1)
	assert.Equal(t, "health", configs[0].Name)
	assert.Equal(t, 3.5, configs[0].Weight)
}

// TestHealthScorer_InPipeline_Composable verifies the health scorer works correctly
// when composed with other scorers in a weighted routing policy.
func TestHealthScorer_InPipeline_Composable(t *testing.T) {
	policy := NewRoutingPolicy("weighted", []ScorerConfig{
		{Name: "prefix-affinity", Weight: 4.0},
		{Name: "queue-depth", Weight: 3.8},
		{Name: "health", Weight: 3.5},
	}, 16, nil)

	snaps := []RoutingSnapshot{
		{ID: "inst_0", KVUtilization: 0.9, PreemptionCount: 0, QueueDepth: 2},  // high KV → low health
		{ID: "inst_1", KVUtilization: 0.3, PreemptionCount: 0, QueueDepth: 10}, // healthy but busy
	}

	req := &Request{ID: "r1", InputTokens: []int{1, 2, 3}}
	assert.NotPanics(t, func() {
		d := policy.Route(req, &RouterState{Snapshots: snaps, Clock: 1000})
		assert.Contains(t, []string{"inst_0", "inst_1"}, d.TargetInstance)
		assert.Len(t, d.Scores, 2)
	})
}
