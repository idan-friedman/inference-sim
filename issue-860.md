**What problem does this solve?**

We recorded a real agentic workload based on SWE-Smith trajectories and converted it to TraceV2 format for replay. When replaying through `blis replay`, all arrival times are pre-baked into the TraceV2 records. For multi-turn closed-loop workloads — where each turn must start only after the previous one completes — this breaks fidelity: the simulator's scheduling decisions have no effect on subsequent round arrival times. If the cluster is under load and turn N takes longer, turn N+1 still arrives at the pre-recorded timestamp rather than at `completion_time + think_time`.

This makes `blis replay` unsuitable for studying how serving policies affect agentic workloads, because the feedback loop between simulator behavior and session pacing is severed.

**Proposed solution**

An option for `blis replay` to reactivate closed-loop session sequencing. Instead of pre-injecting all rounds from the trace upfront, the replay engine would inject only round 0 per session, then inject round N+1 on completion of round N. Token counts and prefix lengths would be read from the trace (not sampled from distributions), and think time would be configurable.

**Which components are affected?**

- [ ] Core simulator (`sim/`)
- [ ] Cluster simulation (`sim/cluster/`)
- [x] Workload generation (`sim/workload/`)
- [ ] KV cache (`sim/kv/`)
- [ ] Decision tracing (`sim/trace/`)
- [x] CLI (`cmd/`)
- [ ] New package needed

**Extension friction check**

- Estimated 3–5 files: `sim/workload/replay.go`, `sim/workload/session.go`, `cmd/replay_cmd.go`, possibly `cmd/root.go`
- Does not require a new interface — `SessionManager` and `SessionBlueprint` already exist. The extension is a new blueprint construction path that sources token counts from trace records rather than sampling from distributions.
- Affects INV-10 (session causality) — the flag must enforce `round[N+1].ArrivalTime >= round[N].CompletionTime + ThinkTimeUs`. Determinism (INV-6) must be preserved since token IDs are still generated from a seed.

**Alternatives considered**

- **Use `blis run` with fitted distributions**: fit input/output token distributions from the trace and use a YAML cohort spec. Loses per-session and per-turn token count fidelity.
- **Pre-bake timestamps with a tuned turn gap**: convert the trace offline and hardcode inter-turn delays before replaying. Timing is a fixed guess, not load-adaptive. Makes it impossible to study how the simulator's own scheduling decisions affect session pacing.
- **Closed-loop replay is preferred** because it combines the real token counts already in TraceV2 with the closed-loop sequencing already in `SessionManager`, without requiring a new data format or new interfaces.

**Relationship to existing work**

None known.
