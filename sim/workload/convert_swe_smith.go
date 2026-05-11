package workload

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// sweSmithItem is one trajectory in swe_smith_workload.json.
type sweSmithItem struct {
	Messages []sweSmithMessage `json:"messages"`
	Metadata sweSmithMetadata  `json:"metadata"`
}

type sweSmithMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type sweSmithMetadata struct {
	InstanceID string `json:"instance_id"`
	TrajID     string `json:"traj_id"`
}

// sweSmithSharedPrefixGroup is the prefix group key used for the shared system
// prompt in SWE-Smith workloads. All trajectories in swe_smith_workload.json
// use the same system message, so round-0 records share this prefix group.
// This causes BLIS to generate one random token sequence for the system prompt
// and reuse it across all sessions, correctly modelling cross-session KV cache
// hits on the system prompt (the dominant source of cache hits in production).
const sweSmithSharedPrefixGroup = "swe_smith_system_prompt"

// Token estimation constants derived from empirical measurement of
// swe_smith_workload.json against the Mistral-Small-24B tokenizer (n=20 trajectories):
//
//   system messages:    4.23 chars/token  → charsPerTokenSystem    = 4
//   user messages:      3.02 chars/token  → charsPerTokenUser      = 3
//   assistant messages: 3.98 chars/token  → charsPerTokenAssistant = 4
//
// User messages contain code, tool outputs, and stack traces that tokenize more
// densely than prose (~3 chars/token vs the generic 4 chars/token heuristic).
// Using role-aware constants reduces the per-session token undercount from ~26%
// to <5%, which is necessary to correctly model KV cache pressure.
//
// The prefix accumulates content from all three roles. We track role-specific
// char counts separately and sum their token estimates.
const (
	charsPerTokenSystem    = 4
	charsPerTokenUser      = 3
	charsPerTokenAssistant = 4
)

// ConvertSWESmith converts swe_smith_workload.json to TraceV2 records.
//
// Each trajectory becomes a closed-loop session. Turns are derived from the
// alternating user/assistant message pairs: for turn k, the prefix is all
// messages accumulated before the current user message, and the input is the
// current user message only (suffix). OutputTokens is the assistant response.
//
// Token counts are estimated from character length (chars/4), which is a
// reasonable approximation for mixed English/code content.
//
// Arrival times: all sessions start at t=0 (N concurrent agents). Turn k
// within a session arrives at k × turnGapUs microseconds, giving BLIS room
// to process each turn before the next is injected.
//
// Prefix group strategy: round-0 records use sweSmithSharedPrefixGroup so that
// all sessions share the same synthetic system-prompt token sequence. This
// models the real cluster behaviour where the system prompt is cached on every
// worker from the very first request. Rounds 1+ use sessionID as prefix group
// because their accumulated prefix is session-specific.
//
// limit caps the number of trajectories converted (0 = all).
func ConvertSWESmith(path string, limit int, turnGapUs int64) (*TraceHeader, []TraceRecord, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("swe-smith path must not be empty")
	}
	if turnGapUs <= 0 {
		return nil, nil, fmt.Errorf("turn-gap-us must be positive, got %d", turnGapUs)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading swe-smith workload: %w", err)
	}

	var items []sweSmithItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, nil, fmt.Errorf("parsing swe-smith workload: %w", err)
	}
	if len(items) == 0 {
		return nil, nil, fmt.Errorf("swe-smith workload is empty: %s", path)
	}

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	records := make([]TraceRecord, 0, len(items)*20) // ~20 turns/trajectory on average
	reqID := 0

	for _, item := range items {
		// TrajID is the primary session key: each trajectory is one session.
		// InstanceID identifies the problem, but multiple trajectories may attempt
		// the same instance — using InstanceID as session key causes duplicate
		// round_index=0 entries when the same instance appears more than once.
		sessionID := item.Metadata.TrajID
		if sessionID == "" {
			sessionID = item.Metadata.InstanceID
		}
		if sessionID == "" {
			sessionID = fmt.Sprintf("traj-%d", reqID)
		}

		msgs := item.Messages
		if len(msgs) == 0 {
			continue
		}

		// Accumulate prefix token estimates as we walk through the conversation.
		// We track char counts per role separately to apply role-aware constants.
		// Prefix = all content seen before the current user turn.
		prefixTokens := 0
		roundIndex := 0

		for i := 0; i < len(msgs); i++ {
			msg := msgs[i]

			if msg.Role == "system" {
				prefixTokens += len(msg.Content) / charsPerTokenSystem
				continue
			}

			if msg.Role == "user" {
				inputTokens := max1(len(msg.Content)/charsPerTokenUser, 1)

				// Find the following assistant message (output).
				outputTokens := 1
				if i+1 < len(msgs) && msgs[i+1].Role == "assistant" {
					outputTokens = max1(len(msgs[i+1].Content)/charsPerTokenAssistant, 1)
				}

				// Round 0's prefix is the system message, shared across all sessions.
				// Subsequent rounds carry session-specific accumulated context.
				prefixGroup := sessionID
				if roundIndex == 0 {
					prefixGroup = sweSmithSharedPrefixGroup
				}

				records = append(records, TraceRecord{
					RequestID:     reqID,
					SessionID:     sessionID,
					RoundIndex:    roundIndex,
					PrefixGroup:   prefixGroup,
					PrefixLength:  prefixTokens,
					Streaming:     true,
					InputTokens:   inputTokens,
					OutputTokens:  outputTokens,
					ArrivalTimeUs: int64(roundIndex) * turnGapUs,
					SendTimeUs:    int64(roundIndex) * turnGapUs,
					Status:        "ok",
				})

				// Advance prefix: add current user message + assistant response.
				prefixTokens += inputTokens + outputTokens
				roundIndex++
				reqID++

				// Skip the assistant message we already consumed above.
				if i+1 < len(msgs) && msgs[i+1].Role == "assistant" {
					i++
				}
				continue
			}

			// assistant message not consumed above (shouldn't happen in well-formed data)
			if msg.Role == "assistant" {
				prefixTokens += max1(len(msg.Content)/charsPerTokenAssistant, 1)
			}
		}
	}

	if len(records) == 0 {
		return nil, nil, fmt.Errorf("no turns extracted from swe-smith workload")
	}

	header := &TraceHeader{
		Version:   2,
		TimeUnit:  "us",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Mode:      "real",
	}

	return header, records, nil
}

// max1 returns the larger of a and b, with a minimum of 1.
func max1(a, b int) int {
	if a > b {
		return a
	}
	return b
}
