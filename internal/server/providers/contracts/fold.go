package contracts

import "time"

// ConversationContractDeliverable is the fixed deliverable for the
// auto-opened conversation contract. The UserPromptSubmit hook writes
// an open_contract event with exactly this deliverable on every
// first prompt; the fold deduplicates by (ownerSessionId, deliverable)
// so only one Contract per session is ever created.
const ConversationContractDeliverable = "user agrees conversation has come to a close and there are no loose ends"

// Fold reduces an ordered list of v0.7 per-contract JSONL events into
// the current state of every contract they touch.
//
// Used by MigrateV07ToV08 to determine which v0.7 contracts are still
// open before writing v0.8 open_contract events into session JSONLs.
// v0.8 live contracts use FoldFromSessionJSONL instead.
//
// Pure function — no IO, no clock, no globals.
func Fold(events []Event) map[ContractID]Contract {
	state := make(map[ContractID]Contract)
	for _, ev := range events {
		applyEvent(state, ev)
	}
	return state
}

// applyEvent dispatches one event into the fold.
func applyEvent(state map[ContractID]Contract, ev Event) {
	switch ev.Kind {
	case "contract":
		applyCreation(state, ev)
	case "status_change":
		applyStatusChange(state, ev)
	default:
		// All other v0.7 kinds (criterion_added, question_*, cancel_*,
		// judge_run, etc.) are not surfaced in v0.8. Touch LastEventAt
		// only so the migration can rank contracts by recency.
		touchLastEvent(state, ContractID(ev.ID), eventTime(ev))
	}
}

// touchLastEvent updates LastEventAt on the named contract, leaving all
// other fields untouched.
func touchLastEvent(state map[ContractID]Contract, id ContractID, at time.Time) {
	c, ok := state[id]
	if !ok {
		return
	}
	if !at.IsZero() {
		c.LastEventAt = at
	}
	state[id] = c
}

// applyCreation initialises a contract from a `kind: contract` row.
//
// Conversation-contract dedup rule: if the event's Statement equals
// ConversationContractDeliverable and the same (ownerSessionId, deliverable)
// pair already has an open contract in state, this event is a no-op. A
// conversation contract that has been closed may be re-opened (L2.13 resume).
//
// Non-conversation-contract events with unique IDs are always inserted.
func applyCreation(state map[ContractID]Contract, ev Event) {
	if ev.ID == "" {
		return
	}
	id := ContractID(ev.ID)

	// Dedup: for the fixed conversation-contract deliverable, skip if an
	// open contract for this (ownerSessionId, deliverable) already exists.
	if ev.Statement == ConversationContractDeliverable && ev.Owner != nil {
		ownerSID := ev.Owner.SessionID
		for _, c := range state {
			if c.OwnerSessionID == ownerSID &&
				c.Statement == ConversationContractDeliverable &&
				c.Status == "open" {
				return // idempotent — conversation contract already open for this session
			}
		}
	}

	created := zeroOr(ev.CreatedOn)
	updated := zeroOr(ev.UpdatedOn)
	if updated.IsZero() {
		updated = created
	}
	status := ev.InitialStatus
	if status == "" {
		status = "open"
	}
	// v0.7 multi-value statuses collapse to the v0.8 binary model:
	// "open" stays "open"; every non-open terminal status becomes "closed".
	if !isOpenStatus(status) {
		status = "closed"
	}

	c := Contract{
		ID:          id,
		Statement:   ev.Statement,
		Status:      status,
		CreatedAt:   created,
		UpdatedAt:   updated,
		LastEventAt: updated,
	}
	if ev.Owner != nil {
		c.OwnerSessionID = ev.Owner.SessionID
		if ev.Owner.AgentName != nil {
			c.OwnerAgentName = *ev.Owner.AgentName
		}
	}

	// Preserve LastEventAt from events that arrived before creation.
	if existing, ok := state[id]; ok {
		if !existing.LastEventAt.IsZero() && existing.LastEventAt.After(c.LastEventAt) {
			c.LastEventAt = existing.LastEventAt
		}
	}
	state[id] = c
}

func applyStatusChange(state map[ContractID]Contract, ev Event) {
	id := ContractID(ev.ID)
	c, ok := state[id]
	if !ok {
		// status_change before creation — drop.
		return
	}
	if ev.To != "" {
		if isOpenStatus(ev.To) {
			c.Status = "open"
		} else {
			c.Status = "closed"
		}
	}
	t := eventTime(ev)
	if !t.IsZero() {
		c.UpdatedAt = t
		c.LastEventAt = t
	}
	state[id] = c
}

// isOpenStatus returns true only for "open". Every other v0.7 status
// (delivered_pending_validation, satisfied, cancelled, etc.) is treated
// as closed in the v0.8 binary model.
func isOpenStatus(s string) bool {
	return s == "open"
}

// eventTime returns the most relevant timestamp for the event.
func eventTime(ev Event) time.Time {
	if ev.Kind == "contract" {
		t := zeroOr(ev.UpdatedOn)
		if t.IsZero() {
			t = zeroOr(ev.CreatedOn)
		}
		return t
	}
	return zeroOr(ev.Timestamp)
}

// zeroOr unwraps a *time.Time to a zero time.Time when nil.
func zeroOr(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
