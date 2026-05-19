package contracts

import (
	"log/slog"
	"time"
)

// Fold reduces an ordered list of v0.7 events into the current state of
// every contract they touch. Pure function — no IO, no clock, no
// globals; the only inputs are events and the only output is the folded
// map.
//
// v0.7 event shape: every line has the same fields (timestamp,
// contract_id, status, summary, reasoning, owner, created_by, source).
// No `kind` discriminator. The fold rules are:
//
//   - First event with a non-null `summary` initialises the contract.
//   - status drives open/closed: "delivered" → DELIVERED; everything
//     else → OPEN. Legacy "blocked" events fold as OPEN.
//   - owner: if the event carries a non-null owner string, that becomes
//     the new ownerSessionId (handoff). If owner is null on an update,
//     the prior ownerSessionId is inherited.
//   - reasoning, created_by, source are updated from the most-recent
//     event that carries them.
//   - An event with an empty status is skipped (forward-compat: legacy
//     v0.6 event kinds that do not carry a status field are silently
//     dropped so the daemon stays forward-compatible with old files).
//
// Events are processed in slice order; callers must pre-sort by
// timestamp if their input is unsorted. On-disk JSONL is naturally in
// append order, so the adapter does no sorting.
func Fold(events []Event) map[ContractID]Contract {
	state := make(map[ContractID]Contract)
	for _, ev := range events {
		applyEvent(state, ev)
	}
	return state
}

// applyEvent applies one event into the fold map. Extracted from Fold
// so the provider's incremental refresh path can apply events one at a
// time without rebuilding the full map.
func applyEvent(state map[ContractID]Contract, ev Event) {
	id := ContractID(ev.ContractID)
	if id == "" {
		// v0.6 creation events used "id" not "contract_id"; both are
		// decoded into ContractID via the struct tags. If still empty,
		// skip — no contract to update.
		return
	}

	// Events with no status are legacy v0.6 non-status event kinds
	// (criterion_added, question_asked, judge_run, cancel_requested,
	// etc.). Drop silently — they carry no state we surface in v0.7.
	if ev.Status == "" {
		slog.Debug("contracts fold: skipping event with empty status",
			"contract_id", id, "kind", ev.Kind)
		return
	}

	t := eventTime(ev)

	existing, exists := state[id]

	if !exists {
		// First event for this contract_id initialises the record.
		c := Contract{
			ID:          id,
			Status:      normaliseStatus(ev.Status),
			Reasoning:   ev.Reasoning,
			CreatedBy:   ev.CreatedBy,
			CreatedAt:   t,
			UpdatedAt:   t,
			LastEventAt: t,
		}
		if ev.Summary != nil {
			c.Summary = *ev.Summary
		}
		if ev.Owner != nil {
			c.OwnerSessionID = *ev.Owner
		}
		if ev.Source != nil {
			c.Source = *ev.Source
		}
		state[id] = c
		return
	}

	// Subsequent event: update mutable fields.
	c := existing

	// Status always updates.
	c.Status = normaliseStatus(ev.Status)

	// Summary inherits when the event carries null.
	if ev.Summary != nil {
		c.Summary = *ev.Summary
	}

	// Owner: null means inherit; non-null means handoff.
	if ev.Owner != nil {
		c.OwnerSessionID = *ev.Owner
	}

	// Scalar fields update from most-recent non-empty value.
	if ev.Reasoning != "" {
		c.Reasoning = ev.Reasoning
	}
	if ev.CreatedBy != "" {
		c.CreatedBy = ev.CreatedBy
	}
	if ev.Source != nil {
		c.Source = *ev.Source
	}

	// Timestamps: UpdatedAt and LastEventAt advance; CreatedAt is
	// immutable.
	if !t.IsZero() {
		c.UpdatedAt = t
		c.LastEventAt = t
	}

	state[id] = c
}

// normaliseStatus maps the plugin's write-path status strings to the
// two-value open/closed model. Legacy "blocked" events fold as OPEN.
// Unknown values default to "started" (open) for forward-compat.
func normaliseStatus(s string) string {
	switch s {
	case "delivered":
		return "delivered"
	default:
		// "started", "blocked" (legacy), any unknown future value → open.
		return "started"
	}
}

// eventTime returns the timestamp for an event. Returns zero when no
// timestamp is present.
func eventTime(ev Event) time.Time {
	if ev.Timestamp == nil {
		return time.Time{}
	}
	return *ev.Timestamp
}

// zeroOr unwraps a *time.Time to a zero time.Time when nil.
func zeroOr(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
