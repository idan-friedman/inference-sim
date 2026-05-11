#!/usr/bin/env python3
"""
tokenize_swe_smith.py — Convert swe_smith_workload.json to TraceV2 format
using exact Mistral-Small-24B tokenizer counts instead of char/4 estimation.

Replicates the logic in sim/workload/convert_swe_smith.go but with zero
token-estimation error. Outputs trace.yaml + trace.csv directly.

Usage:
    python3 scripts/tokenize_swe_smith.py \
        --input /path/to/swe_smith_workload.json \
        --output traces/swe_smith_v3 \
        [--limit N] \
        [--turn-gap-us 100000] \
        [--model mistralai/Mistral-Small-24B-Instruct-2501]
"""

import argparse
import csv
import json
import os
import sys
from datetime import datetime, timezone

# Suppress PyTorch-not-found warning from transformers
os.environ.setdefault("TRANSFORMERS_NO_ADVISORY_WARNINGS", "1")

from transformers import AutoTokenizer  # noqa: E402

SWE_SMITH_SHARED_PREFIX_GROUP = "swe_smith_system_prompt"


def main():
    parser = argparse.ArgumentParser(description="Tokenize SWE-Smith workload to TraceV2")
    parser.add_argument("--input", required=True, help="Path to swe_smith_workload.json")
    parser.add_argument("--output", required=True,
                        help="Output prefix (appends .yaml and .csv)")
    parser.add_argument("--limit", type=int, default=0,
                        help="Max trajectories to convert (0=all)")
    parser.add_argument("--turn-gap-us", type=int, default=100_000,
                        help="Microseconds between turns within a session (default: 100000 = 100ms)")
    parser.add_argument("--model", default="mistralai/Mistral-Small-24B-Instruct-2501",
                        help="HuggingFace model name for tokenizer")
    args = parser.parse_args()

    print(f"Loading tokenizer: {args.model} ...", file=sys.stderr)
    tokenizer = AutoTokenizer.from_pretrained(args.model)
    print("Tokenizer loaded.", file=sys.stderr)

    with open(args.input) as f:
        items = json.load(f)
    print(f"Loaded {len(items)} trajectories from {args.input}", file=sys.stderr)

    if args.limit > 0:
        items = items[:args.limit]
        print(f"Limited to {len(items)} trajectories.", file=sys.stderr)

    records = []
    req_id = 0

    for idx, item in enumerate(items):
        if idx % 20 == 0:
            print(f"  Processing trajectory {idx}/{len(items)} ...", file=sys.stderr)

        traj_id = item.get("metadata", {}).get("traj_id", "")
        instance_id = item.get("metadata", {}).get("instance_id", "")
        session_id = traj_id or instance_id or f"traj-{req_id}"

        msgs = item.get("messages", [])
        if not msgs:
            continue

        prefix_tokens = 0
        round_index = 0

        i = 0
        while i < len(msgs):
            msg = msgs[i]
            role = msg["role"]
            content = msg["content"]
            toks = len(tokenizer.encode(content, add_special_tokens=False))

            if role == "system":
                prefix_tokens += toks
                i += 1
                continue

            if role == "user":
                input_tokens = max(toks, 1)

                # Look ahead for assistant response
                output_tokens = 1
                if i + 1 < len(msgs) and msgs[i + 1]["role"] == "assistant":
                    output_tokens = max(
                        len(tokenizer.encode(msgs[i + 1]["content"], add_special_tokens=False)),
                        1,
                    )

                prefix_group = SWE_SMITH_SHARED_PREFIX_GROUP if round_index == 0 else session_id

                records.append({
                    "request_id": req_id,
                    "session_id": session_id,
                    "round_index": round_index,
                    "prefix_group": prefix_group,
                    "prefix_length": prefix_tokens,
                    "streaming": True,
                    "input_tokens": input_tokens,
                    "output_tokens": output_tokens,
                    "arrival_time_us": round_index * args.turn_gap_us,
                    "send_time_us": round_index * args.turn_gap_us,
                    "status": "ok",
                })

                prefix_tokens += input_tokens + output_tokens
                round_index += 1
                req_id += 1

                # Skip the assistant message we consumed above
                if i + 1 < len(msgs) and msgs[i + 1]["role"] == "assistant":
                    i += 1
                i += 1
                continue

            # Unconsumed assistant message (malformed data)
            if role == "assistant":
                prefix_tokens += max(toks, 1)
            i += 1

    if not records:
        print("ERROR: no turns extracted", file=sys.stderr)
        sys.exit(1)

    print(f"Extracted {len(records)} turns from {len(items)} trajectories.", file=sys.stderr)

    # Summary stats
    input_toks = [r["input_tokens"] for r in records]
    prefix_toks = [r["prefix_length"] for r in records]
    total_input = sum(input_toks)
    total_prefix = sum(prefix_toks)
    print(f"Total input tokens: {total_input:,}", file=sys.stderr)
    print(f"Total prefix tokens (sum): {total_prefix:,}", file=sys.stderr)
    input_toks_sorted = sorted(input_toks)
    n = len(input_toks_sorted)
    p50 = input_toks_sorted[n // 2]
    p95 = input_toks_sorted[int(n * 0.95)]
    p99 = input_toks_sorted[int(n * 0.99)]
    print(f"Input tokens: p50={p50:,} p95={p95:,} p99={p99:,}", file=sys.stderr)

    # Write YAML header
    yaml_path = args.output + ".yaml"
    csv_path = args.output + ".csv"
    created_at = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    with open(yaml_path, "w") as f:
        f.write(f"trace_version: 2\n")
        f.write(f"time_unit: us\n")
        f.write(f'created_at: "{created_at}"\n')
        f.write(f"mode: real\n")
        f.write(f"warm_up_requests: 0\n")
    print(f"Wrote header: {yaml_path}", file=sys.stderr)

    # Write CSV — must match the 27-column TraceV2 format exactly (see tracev2.go:traceV2Columns)
    fieldnames = [
        "request_id", "client_id", "tenant_id", "slo_class", "session_id", "round_index",
        "prefix_group", "prefix_length", "streaming", "input_tokens", "output_tokens",
        "text_tokens", "image_tokens", "audio_tokens", "video_tokens", "reason_ratio",
        "model", "deadline_us", "server_input_tokens",
        "arrival_time_us", "send_time_us", "first_chunk_time_us", "last_chunk_time_us",
        "num_chunks", "status", "error_message", "finish_reason",
    ]
    # Populate defaults for columns not set by the converter
    for r in records:
        r.setdefault("client_id", "")
        r.setdefault("tenant_id", "")
        r.setdefault("slo_class", "")
        r.setdefault("text_tokens", 0)
        r.setdefault("image_tokens", 0)
        r.setdefault("audio_tokens", 0)
        r.setdefault("video_tokens", 0)
        r.setdefault("reason_ratio", 0.0)
        r.setdefault("model", "")
        r.setdefault("deadline_us", 0)
        r.setdefault("server_input_tokens", 0)
        r.setdefault("first_chunk_time_us", 0)
        r.setdefault("last_chunk_time_us", 0)
        r.setdefault("num_chunks", 0)
        r.setdefault("error_message", "")
        r.setdefault("finish_reason", "")
    with open(csv_path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(records)
    print(f"Wrote data:   {csv_path}", file=sys.stderr)
    print("Done.", file=sys.stderr)


if __name__ == "__main__":
    main()
