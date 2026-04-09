def build_genome() -> dict:
    """Build genome: two-signal health scorer (KV 0.55 + preemption 0.45), boosted prefix cache and active_request for better load distribution."""
    custom_source = '''
def health_scorer(workers, request, metrics_store, kv_index, cycle_state, params,
                  _state={"prev_pre": {}}):
    """Two-signal scorer: KV cubic penalty (0.55) + preemption delta (0.45)."""
    scores = {}
    pp = _state["prev_pre"]
    kv_thresh = 0.85
    for w in workers:
        m = metrics_store.get(w.address)
        if m is None:
            scores[w.address] = 0.5
            continue
        kv = m.kv_cache_usage
        kv_score = 0.0 if kv >= kv_thresh else 1.0 - (kv / kv_thresh) ** 3
        pre = m.preemption_count if m.preemption_count else 0.0
        prev_p = pp.get(w.address, pre)
        delta = max(0.0, pre - prev_p)
        pp[w.address] = pre
        pre_score = 0.0 if delta > 0 else 1.0
        scores[w.address] = 0.55 * kv_score + 0.45 * pre_score
    return scores
'''
    return {
        "scorers": [
            {"name": "precise_prefix_cache", "weight": 4.0, "params": {}},
            {"name": "no_hit_lru", "weight": 0.6, "params": {}},
            {"name": "active_request", "weight": 3.8, "params": {}},
            {"name": "health_scorer", "weight": 3.5, "params": {}},
        ],