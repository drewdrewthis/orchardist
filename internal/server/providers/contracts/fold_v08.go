package contracts

// fold_v08.go implements the v0.8 ContractFold projection.
//
// v0.8 stores contract lifecycle events as tool_use blocks inside assistant
// message records in session JSONL files (~/.claude/projects/*/<uuid>.jsonl).
// This file provides the pure-fold layer:
//
//   - FoldFromSessionJSONL: fold a single session's records into a Contract map.
//   - ApplySessionRecords: apply records to an existing global state map.
//   - FoldProjectsRecords: fold records from multiple sessions (ProjectsRecord).
//   - ApplyProjectsRecords: apply multi-session records to an existing state map.
//   - NewProjectsAdapter: returns a ProjectsAdapter for scanning the projects root.
//
// ProjectsAdapter lives in projects_adapter.go (adapter + watcher split) and
// is declared separately so tests can inject a different root without touching
// the watcher.

import (
	"encoding/json"
	"time"
)

// FoldFromSessionJSONL folds a slice of SessionRecords for a single
// session into a Contract map. Each open_contract tool_use block
// initialises a Contract keyed by contract id; close_contract closes it.
//
// ownerSessionID is the session UUID whose JSONL these records came from.
// Every Contract produced by an open_contract event in this slice carries
// ownerSessionID as its OwnerSessionID.
//
// Dedup rule: if the same (ownerSessionID, deliverable) pair already has
// an OPEN contract, a subsequent open_contract for the same deliverable is
// a no-op. A close followed by a new open IS a new record.
func FoldFromSessionJSONL(records []SessionRecord, ownerSessionID string) map[ContractID]Contract {
	state := make(map[ContractID]Contract)
	ApplySessionRecords(state, records, ownerSessionID)
	return state
}

// ApplySessionRecords applies a slice of SessionRecords to an existing
// global Contract map. localSessionID is the UUID of the JSONL these
// records came from — used as OwnerSessionID for open_contract events
// and to resolve F2 non-owner abandon closes (aboutSessionId).
func ApplySessionRecords(state map[ContractID]Contract, records []SessionRecord, localSessionID string) {
	for _, rec := range records {
		applySessionRecord(state, rec, localSessionID)
	}
}

// applySessionRecord dispatches one SessionRecord into the fold state.
//
// Assistant records with tool_use content blocks are processed for
// open_contract and close_contract events (the primary v0.8 path).
//
// System records are checked for exit/quit/bye local_command content;
// when matched a virtual close of the open conversation contract is
// synthesised in-memory (L2.11/L2.12).
func applySessionRecord(state map[ContractID]Contract, rec SessionRecord, localSessionID string) {
	// L2.11/L2.12: system/local_command exit records.
	if rec.Type == "system" {
		applyExitRecord(state, rec, localSessionID)
		return
	}

	if rec.Type != "assistant" || rec.Message == nil {
		return
	}
	for _, block := range rec.Message.Content {
		if block.Type != "tool_use" {
			continue
		}
		switch block.Name {
		case "open_contract":
			applyOpenContractBlock(state, block, localSessionID, rec)
		case "close_contract":
			applyCloseContractBlock(state, block)
		}
	}
}

// applyOpenContractBlock handles one open_contract tool_use content block.
func applyOpenContractBlock(state map[ContractID]Contract, block SessionContentBlock, ownerSessionID string, rec SessionRecord) {
	var inp OpenContractInput
	if err := json.Unmarshal(block.Input, &inp); err != nil {
		return // malformed input — skip silently
	}
	if inp.ID == "" {
		return
	}
	id := ContractID(inp.ID)

	// Dedup rule: if (ownerSessionID, deliverable) already has an OPEN
	// contract, treat this as a no-op. A close then re-open IS a new record.
	deliverable := inp.effectiveDeliverable()
	if deliverable != "" {
		for _, c := range state {
			if c.OwnerSessionID == ownerSessionID &&
				c.Statement == deliverable &&
				c.Status == "open" {
				return // already open — idempotent
			}
		}
	}

	// Parse creation timestamp from input; fall back to the record timestamp.
	createdAt := parseRFC3339(inp.CreatedAt)
	if createdAt.IsZero() && rec.Timestamp != "" {
		createdAt = parseRFC3339(rec.Timestamp)
	}

	c := Contract{
		ID:             id,
		Statement:      deliverable,
		OwnerSessionID: ownerSessionID,
		Status:         "open",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		LastEventAt:    createdAt,
	}
	state[id] = c
}

// applyCloseContractBlock handles one close_contract tool_use content block.
// Closes by contract id regardless of which session jsonl the close lives in
// (supports the F2 non-owner abandon case where aboutSessionId != owner).
func applyCloseContractBlock(state map[ContractID]Contract, block SessionContentBlock) {
	var inp CloseContractInput
	if err := json.Unmarshal(block.Input, &inp); err != nil {
		return // malformed input — skip silently
	}
	if inp.ID == "" {
		return
	}
	id := ContractID(inp.ID)

	closedAt := parseRFC3339(inp.ClosedAt)

	c, ok := state[id]
	if !ok {
		// Close arrived before open (cross-jsonl case: the owner's jsonl
		// hasn't been scanned yet). Create a minimal closed placeholder.
		c = Contract{
			ID:           id,
			Status:       "closed",
			ClosedReason: inp.ClosedReason,
		}
		if !closedAt.IsZero() {
			c.UpdatedAt = closedAt
			c.LastEventAt = closedAt
		}
		state[id] = c
		return
	}

	if c.Status == "closed" {
		return // already closed — idempotent
	}
	c.Status = "closed"
	c.ClosedReason = inp.ClosedReason
	if !closedAt.IsZero() {
		c.UpdatedAt = closedAt
		c.LastEventAt = closedAt
	}
	state[id] = c
}

// parseRFC3339 parses an RFC 3339 timestamp (with or without nanoseconds).
// Returns a zero time.Time on empty input or parse failure.
func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// ---------------------------------------------------------------------------
// Multi-session fold (ProjectsRecord)
// ---------------------------------------------------------------------------

// ProjectsRecord pairs a SessionRecord with the session UUID it came from.
// The ProjectsAdapter emits these so callers know which session owns each
// record without re-deriving the UUID from the file path.
type ProjectsRecord struct {
	SessionID string
	Record    SessionRecord
}

// FoldProjectsRecords folds a slice of ProjectsRecords (from multiple
// session jsonls) into a single global Contract map indexed by contract id.
// Cross-jsonl close resolution works because all records share the same map.
func FoldProjectsRecords(records []ProjectsRecord) map[ContractID]Contract {
	state := make(map[ContractID]Contract)
	ApplyProjectsRecords(state, records)
	return state
}

// ApplyProjectsRecords applies a slice of ProjectsRecords to an existing
// state map. Used for incremental updates (follow-from-offsets path) to
// avoid a full rebuild on every fsnotify tick.
func ApplyProjectsRecords(state map[ContractID]Contract, records []ProjectsRecord) {
	for _, pr := range records {
		applySessionRecord(state, pr.Record, pr.SessionID)
	}
}
