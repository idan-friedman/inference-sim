"""Baseline genome program for SkyDiscover scorer evolution.

The ``EVOLVE-BLOCK-START/END`` markers define the region that
SkyDiscover's LLM will mutate.  ``run_experiment()`` is the entry
point called by the evaluation harness.
"""

from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request

# Config is propagated via env vars set by run_discovery.py.
# SkyDiscover evaluator runs in-process, so env vars are visible.
ADMIN_URL = os.environ.get("EVOLVE_ADMIN_URL", "http://localhost:9099")
BASE_URL = os.environ.get("EVOLVE_BASE_URL", "http://localhost:8000")
DATA_PATH = os.environ.get("EVOLVE_DATA_PATH", "workload.json")
MODEL_NAME = os.environ.get("EVOLVE_MODEL_NAME", "Qwen/Qwen3-30B-A3B")
NUM_AGENTS = int(os.environ.get("EVOLVE_NUM_AGENTS", "8"))
TRAJECTORIES_PER_AGENT = int(os.environ.get("EVOLVE_TRAJECTORIES_PER_AGENT", "3"))
STABILIZATION_DELAY = int(os.environ.get("EVOLVE_STABILIZATION_DELAY", "5"))


# EVOLVE-BLOCK-START
def build_genome() -> dict:
    """Build the scorer genome. SkyDiscover mutates this function."""
    return {
        "scorers": [
            {"name": "precise_prefix_cache", "weight": 3.0, "params": {}},
            {"name": "kv_cache", "weight": 2.0, "params": {}},
            {"name": "queue", "weight": 2.0, "params": {}},
        ],
        "custom_scorers": {},
        "picker": "max_score",
        "picker_top_k": 1,
        "saturation": {
            "queue_depth_threshold": 5,
            "kv_cache_util_threshold": 0.8,
        },
    }


# EVOLVE-BLOCK-END


def run_experiment(**kwargs: object) -> dict:
    """Entry point called by SkyDiscover evaluator.

    Returns dict with p50_trajectory_time, success_rate, etc.
    Exceptions are NOT caught here -- the evaluator wrapper handles them.
    """
    genome = build_genome()

    # Validate custom scorer code compiles
    for name, source in genome.get("custom_scorers", {}).items():
        compile(source, f"<{name}>", "exec")

    # Hot-swap the gateway config
    payload = json.dumps(genome).encode()
    req = urllib.request.Request(  # noqa: S310
        f"{ADMIN_URL}/admin/genome",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:  # noqa: S310
            if resp.status != 200:
                return {"p50_trajectory_time": 9999, "success_rate": 0}
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError):
        return {"p50_trajectory_time": 9999, "success_rate": 0}

    # Wait for stabilization
    time.sleep(STABILIZATION_DELAY)

    # Run workload
    from inference_perf.send_swe_smith_multiple_trajectory import (
        run_multiple_trajectory,
    )

    perf_result = run_multiple_trajectory(
        base_url=BASE_URL,
        data_path=DATA_PATH,
        num_agents=NUM_AGENTS,
        model_name=MODEL_NAME,
        trajectories_per_agent=TRAJECTORIES_PER_AGENT,
        max_turns=15,
        time_scale=1.0,
    )

    # Extract metrics
    reports = perf_result.get("reports", {})
    traj_metrics = reports.get("trajectory_time_metrics", {})
    traj_time = traj_metrics.get("trajectory_time", {})
    p50_traj_time = traj_time.get("median", 9999)

    summary = reports.get("summary_lifecycle_metrics", {})
    successes = summary.get("successes", {})
    failures = summary.get("failures", {})
    success_count = successes.get("count", 0)
    failure_count = failures.get("count", 0)
    total = success_count + failure_count
    success_rate = success_count / total if total > 0 else 0

    throughput = successes.get("throughput", {})
    tokens_per_sec = throughput.get("total_tokens_per_sec", 0)

    return {
        "p50_trajectory_time": p50_traj_time,
        "success_rate": success_rate,
        "tokens_per_sec": tokens_per_sec,
        "success_count": success_count,
        "failure_count": failure_count,
    }
