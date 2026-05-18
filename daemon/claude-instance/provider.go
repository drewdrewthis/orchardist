// provider.go — state derivation for ClaudeInstance.
//
// This file contains the pure state machine (DeriveState + deriveState)
// and the ClassifyState classifier that converts a slice of jsonl Records
// into an InstanceState. This is the "functional core" of the domain; no
// I/O happens here. All I/O is pushed to adapter.go.
package claudeinstance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"syscall"
	"time"
)

// ─── Liveness ─────────────────────────────────────────────────────────────────

// OSLivenessChecker uses the standard signal-0 trick to ask the kernel
// whether a pid is alive without sending a real signal.
type OSLivenessChecker struct{}

// IsAlive returns true when sending signal 0 to pid succeeds.
// Returns false for pid <= 0.
func (OSLivenessChecker) IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// ─── Records ─────────────────────────────────────────────────────────────────

// ContentItem is one element of message.content[].
type ContentItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
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

// SystemInfo holds the subtype for system-type records.
type SystemInfo struct {
	Subtype string `json:"subtype"`
}

// Record is the fully-decoded shape of one jsonl line. Sidechain records
// (IsSidechain == true) are stripped by readRecordsFromPath before
// ClassifyState sees them.
type Record struct {
	Timestamp   time.Time   `json:"-"`
	Type        string      `json:"type"`
	IsSidechain bool        `json:"isSidechain"`
	Message     *Message    `json:"message"`
	System      *SystemInfo `json:"system"`
}

// ─── Snapshot ─────────────────────────────────────────────────────────────────

// JsonlStateSnapshot is the output of ClassifyState. Pure data.
type JsonlStateSnapshot struct {
	State             InstanceState
	InflightToolCount int
	LastActivityAt    time.Time
	Model             string
}

// ─── State derivation ─────────────────────────────────────────────────────────

// DeriveState encapsulates the inputs needed to derive one ClaudeInstance's
// state. Defined here so tests can construct it without importing provider.
type DeriveState struct {
	Cwd         string
	SessionUUID string
	Pid         int
	StaleAfter  time.Duration
	Snapshot    SnapshotReader
	Liveness    LivenessChecker
	Clock       func() time.Time
}

// deriveState derives InstanceState + JsonlStateSnapshot from the given
// inputs. It is the single state-derivation entry point for the join logic.
func deriveState(ctx context.Context, d DeriveState) (InstanceState, JsonlStateSnapshot) {
	liveness := d.Liveness
	if liveness == nil {
		liveness = OSLivenessChecker{}
	}

	// Dead pid → NoClaude.
	if d.Pid > 0 && !liveness.IsAlive(d.Pid) {
		return StateNoClaude, JsonlStateSnapshot{}
	}

	// No jsonl inputs → idle for live/unknown pids.
	if d.Snapshot == nil || d.Cwd == "" || d.SessionUUID == "" {
		return StateIdle, JsonlStateSnapshot{}
	}

	records, ok := d.Snapshot.ReadSnapshot(ctx, d.Cwd, d.SessionUUID)
	if !ok {
		return StateIdle, JsonlStateSnapshot{}
	}

	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	snap := classifyState(records, clock())
	return snap.State, snap
}

// classifyState derives the session state from a slice of records in file
// order. Records with isSidechain==true must be filtered before this call.
// now anchors timestamps when no usable timestamps exist.
func classifyState(records []Record, now time.Time) JsonlStateSnapshot {
	const orphanLookback = 100

	if len(records) == 0 {
		return JsonlStateSnapshot{
			State: StateIdle,
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
	for _, name := range openTools {
		if name == "AskUserQuestion" {
			snap := JsonlStateSnapshot{
				State:             StateInput,
				InflightToolCount: inflightCount,
				LastActivityAt:    lastActivityAt,
			}
			fillUsage(&snap, records)
			return snap
		}
	}

	tail := lastSignificantRecord(records)
	state := stateFromTail(tail, inflightCount)

	snap := JsonlStateSnapshot{
		State:             state,
		InflightToolCount: inflightCount,
		LastActivityAt:    lastActivityAt,
	}
	fillUsage(&snap, records)
	return snap
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

// currentTurnStart returns the index of the first record in the current turn.
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

// stateFromTail maps the tail record to an InstanceState.
func stateFromTail(tail *Record, inflightCount int) InstanceState {
	if tail == nil {
		return StateIdle
	}
	switch tail.Type {
	case "assistant":
		if tail.Message != nil {
			switch tail.Message.StopReason {
			case "end_turn", "max_tokens":
				return StateIdle
			case "tool_use":
				return StateWorking
			}
		}
		if inflightCount > 0 {
			return StateWorking
		}
		return StateIdle
	case "user":
		return StateWorking
	case "system":
		if tail.System != nil {
			sub := tail.System.Subtype
			if sub == "stop_hook_summary" || sub == "turn_duration" {
				return StateIdle
			}
		}
	}
	if inflightCount > 0 {
		return StateWorking
	}
	return StateIdle
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

// fillUsage populates Model from the most-recent assistant record that has
// a message.model field.
func fillUsage(snap *JsonlStateSnapshot, records []Record) {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Type == "assistant" && r.Message != nil && r.Message.Model != "" {
			snap.Model = r.Message.Model
			return
		}
	}
}

// ─── Decoder (internal) ───────────────────────────────────────────────────────

// readRecordsFromPath reads and decodes all non-sidechain records from
// ~/.claude/projects/<encodedCwd>/<sessionUUID>.jsonl.
//
// Tolerances match the original claudeinstance package:
//   - Missing file → (nil, nil)
//   - Lines > 1 MB → dropped in isolation
//   - Malformed JSON → skipped
func readRecordsFromPath(projectsDir, cwd, sessionUUID string) ([]Record, error) {
	path := encodeCwdPath(projectsDir, cwd, sessionUUID)
	f, err := os.Open(path) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	const maxLine = 1024 * 1024
	reader := bufio.NewReader(f)

	var records []Record
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && len(line) <= maxLine {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				if r, ok := decodeLine(trimmed); ok && !r.IsSidechain {
					records = append(records, r)
				}
			}
		}
		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return records, err
		}
	}
}

// decodeLine parses one jsonl line into a Record.
func decodeLine(line []byte) (Record, bool) {
	var raw struct {
		Timestamp   string      `json:"timestamp"`
		Type        string      `json:"type"`
		IsSidechain bool        `json:"isSidechain"`
		Message     *Message    `json:"message"`
		System      *SystemInfo `json:"system"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return Record{}, false
	}

	var ts time.Time
	if raw.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
			ts = t
		} else if t, err := time.Parse(time.RFC3339, raw.Timestamp); err == nil {
			ts = t
		}
	}

	return Record{
		Timestamp:   ts,
		Type:        raw.Type,
		IsSidechain: raw.IsSidechain,
		Message:     raw.Message,
		System:      raw.System,
	}, true
}
