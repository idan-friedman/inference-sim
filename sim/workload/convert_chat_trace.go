package workload

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// chatTraceRecord is one line from a multi-turn chat production trace JSONL file.
// The trace uses block_size=16 tokens per hash_id entry.
type chatTraceRecord struct {
	ChatID       int     `json:"chat_id"`
	ParentChatID int     `json:"parent_chat_id"` // -1 for first turn
	Timestamp    float64 `json:"timestamp"`       // seconds since trace start
	InputLength  int     `json:"input_length"`    // total input tokens including cached context
	OutputLength int     `json:"output_length"`   // output tokens
	Type         string  `json:"type"`            // "text", "image", "search"
	Turn         int     `json:"turn"`            // 1-indexed turn within session
	HashIDs      []int   `json:"hash_ids"`        // KV block hash IDs, len ≈ input_length/16
}

const chatTraceBlockSize = 16 // tokens per hash_id block in blksz_16 traces

// ConvertChatTrace converts a multi-turn chat production trace JSONL file to TraceV2 format.
// limit caps the number of output records (0 = no limit).
// Records are sorted by arrival time and prefix lengths are derived from
// the common block prefix between consecutive turns in the same session.
func ConvertChatTrace(path string, limit int) (*TraceHeader, []TraceRecord, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("chat trace path must not be empty")
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening chat trace: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Parse all records.
	var raw []chatTraceRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // 4 MiB per line (large hash_ids)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r chatTraceRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, nil, fmt.Errorf("parsing chat trace record: %w", err)
		}
		if r.Turn < 1 {
			return nil, nil, fmt.Errorf("record chat_id=%d has invalid turn=%d", r.ChatID, r.Turn)
		}
		if r.InputLength < 0 || r.OutputLength < 0 {
			return nil, nil, fmt.Errorf("record chat_id=%d has negative token count", r.ChatID)
		}
		raw = append(raw, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading chat trace: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("chat trace is empty: %s", path)
	}

	// Build lookup: chat_id → record index.
	byID := make(map[int]int, len(raw))
	for i, r := range raw {
		byID[r.ChatID] = i
	}

	// Resolve session root for each record: follow parent_chat_id chain to the
	// record with parent_chat_id == -1. Cap depth to avoid infinite loops on
	// malformed data.
	sessionRoot := make(map[int]int, len(raw)) // chat_id → root chat_id
	for _, r := range raw {
		root := r.ChatID
		for depth := 0; depth < 100; depth++ {
			idx, ok := byID[root]
			if !ok || raw[idx].ParentChatID == -1 {
				break
			}
			root = raw[idx].ParentChatID
		}
		sessionRoot[r.ChatID] = root
	}

	// Group records by session root, then sort each session by turn.
	sessions := make(map[int][]int) // root chat_id → slice of raw indices
	for i, r := range raw {
		root := sessionRoot[r.ChatID]
		sessions[root] = append(sessions[root], i)
	}
	for root := range sessions {
		idxs := sessions[root]
		sort.Slice(idxs, func(a, b int) bool {
			return raw[idxs[a]].Turn < raw[idxs[b]].Turn
		})
		sessions[root] = idxs
	}

	// Build TraceRecords with prefix lengths derived from hash_id overlap.
	records := make([]TraceRecord, 0, len(raw))
	reqID := 0
	for root, idxs := range sessions {
		sessionID := fmt.Sprintf("session-%d", root)
		var prevHashIDs []int

		for _, idx := range idxs {
			r := raw[idx]

			// Compute prefix length: count of leading hash_ids that match
			// the previous turn's hash_ids (longest common prefix from start).
			prefixBlocks := 0
			if prevHashIDs != nil {
				prefixBlocks = commonPrefixLen(r.HashIDs, prevHashIDs)
			}
			prefixTokens := prefixBlocks * chatTraceBlockSize

			// Guard: prefixTokens must not exceed input_length.
			if prefixTokens > r.InputLength {
				prefixTokens = r.InputLength
			}
			suffixTokens := r.InputLength - prefixTokens

			records = append(records, TraceRecord{
				RequestID:     reqID,
				SessionID:     sessionID,
				RoundIndex:    r.Turn - 1, // 0-indexed
				PrefixGroup:   sessionID,
				PrefixLength:  prefixTokens,
				Streaming:     true,
				InputTokens:   suffixTokens,
				OutputTokens:  r.OutputLength,
				ArrivalTimeUs: int64(r.Timestamp * 1e6),
				SendTimeUs:    int64(r.Timestamp * 1e6),
				Status:        "ok",
			})

			prevHashIDs = r.HashIDs
			reqID++
		}
	}

	// Sort by arrival time (preserving original temporal ordering).
	sort.Slice(records, func(i, j int) bool {
		if records[i].ArrivalTimeUs != records[j].ArrivalTimeUs {
			return records[i].ArrivalTimeUs < records[j].ArrivalTimeUs
		}
		return records[i].RequestID < records[j].RequestID
	})

	// Re-sequence RequestIDs after sort.
	for i := range records {
		records[i].RequestID = i
	}

	// Apply limit.
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	header := &TraceHeader{
		Version:   2,
		TimeUnit:  "us",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Mode:      "real",
	}

	return header, records, nil
}

// commonPrefixLen returns the number of leading elements shared between a and b.
func commonPrefixLen(a, b []int) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
