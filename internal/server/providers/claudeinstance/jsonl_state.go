package claudeinstance

import (
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// JsonlStateSnapshot is the output of ClassifyState. Pure data; no I/O.
type JsonlStateSnapshot struct {
	State              graphql.InstanceState
	InflightToolCount  int
	StateChangedAt     time.Time
	LastActivityAt     time.Time
	Model              string
	StopReason         string
	InputTokens        uint64
	OutputTokens       uint64
	CacheCreationInput uint64
	CacheReadInput     uint64
}

// ContentItem is one element of message.content[].
type ContentItem struct {
	Type      string `json:"type"`        // "tool_use" | "tool_result" | "text" | ...
	ID        string `json:"id"`          // tool_use id
	Name      string `json:"name"`        // tool name (e.g. "Bash", "AskUserQuestion")
	ToolUseID string `json:"tool_use_id"` // for tool_result items
}

// MessageUsage holds token counts from message.usage.
type MessageUsage struct {
	InputTokens        uint64 `json:"input_tokens"`
	OutputTokens       uint64 `json:"output_tokens"`
	CacheCreationInput uint64 `json:"cache_creation_input_tokens"`
	CacheReadInput     uint64 `json:"cache_read_input_tokens"`
}

// Message is the nested message object in assistant/user records.
type Message struct {
	Model      string        `json:"model"`
	StopReason string        `json:"stop_reason"`
	Usage      *MessageUsage `json:"usage"`
	Content    []ContentItem `json:"content"`
}

// Attachment holds the hookEvent for attachment-type records.
type Attachment struct {
	HookEvent string `json:"hookEvent"`
}

// SystemInfo holds the subtype for system-type records.
type SystemInfo struct {
	Subtype string `json:"subtype"`
}

// Record is the fully-decoded shape of one jsonl line. Fields are
// parsed on demand; unknown record types pass through as-is so the
// classifier can skip them silently.
type Record struct {
	Timestamp   time.Time   `json:"-"` // parsed from the raw string
	Type        string      `json:"type"`
	IsSidechain bool        `json:"isSidechain"`
	Message     *Message    `json:"message"`
	Attachment  *Attachment `json:"attachment"`
	System      *SystemInfo `json:"system"`
}

// turnBoundary reports whether r marks the end of a conversation turn.
func turnBoundary(r Record) bool {
	if r.Type == "assistant" && r.Message != nil {
		sr := r.Message.StopReason
		if sr == "end_turn" || sr == "max_tokens" {
			return true
		}
	}
	if r.Type == "system" && r.System != nil {
		sub := r.System.Subtype
		if sub == "turn_duration" || sub == "stop_hook_summary" {
			return true
		}
	}
	return false
}

// currentTurnStart returns the index of the first record in the current
// turn. lookback limits orphan blast radius to the last N records.
func currentTurnStart(records []Record, lookback int) int {
	n := len(records)
	start := 0
	if n > lookback {
		start = n - lookback
	}
	boundary := start
	for i := n - 1; i >= start; i-- {
		if turnBoundary(records[i]) {
			boundary = i + 1
			break
		}
	}
	return boundary
}

// ClassifyState derives the session state from a slice of records in
// file order. Records with isSidechain==true MUST be filtered out before
// calling this function. now anchors StateChangedAt when no usable timestamps exist.
//
// File order (not timestamp order) is authoritative — timestamps are
// non-monotonic ~25% of the time.
func ClassifyState(records []Record, now time.Time) JsonlStateSnapshot {
	const orphanLookback = 100

	if len(records) == 0 {
		// LastActivityAt stays zero — an empty transcript has no activity to
		// report. Setting it to `now` would manufacture a false fresh-activity
		// signal that ticks the subscription every refresh
		// (https://github.com/drewdrewthis/orchardist/pull/606#discussion_r3243103664).
		return JsonlStateSnapshot{
			State:          graphql.InstanceStateIdle,
			StateChangedAt: now,
		}
	}

	lastActivityAt := maxTimestamp(records, orphanLookback)
	turnStart := currentTurnStart(records, orphanLookback)
	currentTurn := records[turnStart:]

	// Walk current turn: +1 per tool_use, -1 per matching tool_result.
	openTools := map[string]string{} // id → name
	for _, r := range currentTurn {
		if r.Type == "assistant" && r.Message != nil {
			for _, c := range r.Message.Content {
				if c.Type == "tool_use" && c.ID != "" {
					openTools[c.ID] = c.Name
				}
			}
		}
		if r.Type == "user" && r.Message != nil {
			for _, c := range r.Message.Content {
				if c.Type == "tool_result" && c.ToolUseID != "" {
					delete(openTools, c.ToolUseID)
				}
			}
		}
	}

	inflightCount := len(openTools)

	// Open AskUserQuestion → input. Any other open tool_use → working.
	// This is the only jsonl-derivable input source; no fallback heuristics.
	for _, name := range openTools {
		if name == "AskUserQuestion" {
			snap := JsonlStateSnapshot{
				State:             graphql.InstanceStateInput,
				InflightToolCount: inflightCount,
				StateChangedAt:    stateChangedAt(records, graphql.InstanceStateInput, turnStart, now),
				LastActivityAt:    lastActivityAt,
			}
			fillUsage(&snap, records)
			return snap
		}
	}

	tail := lastSignificantRecord(records)
	state := deriveStateFromTail(tail, inflightCount)

	snap := JsonlStateSnapshot{
		State:             state,
		InflightToolCount: inflightCount,
		StateChangedAt:    stateChangedAt(records, state, turnStart, now),
		LastActivityAt:    lastActivityAt,
	}
	fillUsage(&snap, records)
	return snap
}

// lastSignificantRecord walks backward to find the last assistant, user,
// or system (with subtype) record.
func lastSignificantRecord(records []Record) *Record {
	for i := len(records) - 1; i >= 0; i-- {
		r := &records[i]
		switch r.Type {
		case "assistant", "user":
			return r
		case "system":
			if r.System != nil && r.System.Subtype != "" {
				return r
			}
		}
	}
	return nil
}

// deriveStateFromTail maps the tail record to a state.
func deriveStateFromTail(tail *Record, inflightCount int) graphql.InstanceState {
	if tail == nil {
		return graphql.InstanceStateIdle
	}
	switch tail.Type {
	case "assistant":
		if tail.Message != nil {
			switch tail.Message.StopReason {
			case "end_turn", "max_tokens":
				return graphql.InstanceStateIdle
			case "tool_use":
				return graphql.InstanceStateWorking
			}
		}
		if inflightCount > 0 {
			return graphql.InstanceStateWorking
		}
		return graphql.InstanceStateIdle
	case "user":
		return graphql.InstanceStateWorking
	case "system":
		if tail.System != nil {
			sub := tail.System.Subtype
			if sub == "stop_hook_summary" || sub == "turn_duration" {
				return graphql.InstanceStateIdle
			}
		}
	}
	if inflightCount > 0 {
		return graphql.InstanceStateWorking
	}
	return graphql.InstanceStateIdle
}

// maxTimestamp returns the max timestamp in the last lookback records.
func maxTimestamp(records []Record, lookback int) time.Time {
	start := 0
	if len(records) > lookback {
		start = len(records) - lookback
	}
	var max time.Time
	for i := start; i < len(records); i++ {
		if records[i].Timestamp.After(max) {
			max = records[i].Timestamp
		}
	}
	return max
}

// stateChangedAt scans backward to find the boundary where the state
// changed, returning that record's timestamp. Falls back to turnStart
// or the first record's timestamp.
func stateChangedAt(records []Record, current graphql.InstanceState, turnStart int, now time.Time) time.Time {
	if len(records) == 0 {
		return now
	}
	for i := len(records) - 2; i >= 0; i-- {
		r := records[i]
		st := deriveStateFromTail(&r, 0)
		if st != current {
			if i+1 < len(records) && !records[i+1].Timestamp.IsZero() {
				return records[i+1].Timestamp
			}
			break
		}
	}
	if turnStart < len(records) && !records[turnStart].Timestamp.IsZero() {
		return records[turnStart].Timestamp
	}
	if !records[0].Timestamp.IsZero() {
		return records[0].Timestamp
	}
	return now
}

// fillUsage populates model and token fields from the most-recent assistant
// record that has a message.usage field.
func fillUsage(snap *JsonlStateSnapshot, records []Record) {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Type == "assistant" && r.Message != nil && r.Message.Usage != nil {
			snap.Model = r.Message.Model
			snap.StopReason = r.Message.StopReason
			snap.InputTokens = r.Message.Usage.InputTokens
			snap.OutputTokens = r.Message.Usage.OutputTokens
			snap.CacheCreationInput = r.Message.Usage.CacheCreationInput
			snap.CacheReadInput = r.Message.Usage.CacheReadInput
			return
		}
	}
}
