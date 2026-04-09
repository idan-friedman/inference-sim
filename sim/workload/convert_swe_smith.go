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

		// Accumulate prefix chars as we walk through the conversation.
		// Prefix = all content seen before the current user turn.
		prefixChars := 0
		roundIndex := 0

		for i := 0; i < len(msgs); i++ {
			msg := msgs[i]

			if msg.Role == "system" {
				prefixChars += len(msg.Content)
				continue
			}

			if msg.Role == "user" {
				inputChars := len(msg.Content)

				// Find the following assistant message (output).
				outputChars := 0
				if i+1 < len(msgs) && msgs[i+1].Role == "assistant" {
					outputChars = len(msgs[i+1].Content)
				}

				records = append(records, TraceRecord{
					RequestID:     reqID,
					SessionID:     sessionID,
					RoundIndex:    roundIndex,
					PrefixGroup:   sessionID,
					PrefixLength:  prefixChars / 4,
					Streaming:     true,
					InputTokens:   max1(inputChars/4, 1),
					OutputTokens:  max1(outputChars/4, 1),
					ArrivalTimeUs: int64(roundIndex) * turnGapUs,
					SendTimeUs:    int64(roundIndex) * turnGapUs,
					Status:        "ok",
				})

				// Advance prefix: add current user message + assistant response.
				prefixChars += inputChars + outputChars
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
				prefixChars += len(msg.Content)
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
