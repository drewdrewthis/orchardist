package contracts

import (
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
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
		return
	}

	t := eventTime(ev)

	c, exists := state[id]
	if !exists {
		c = Contract{ID: id, CreatedAt: t}
	}

	c.Status = statusFromRaw(ev.Status)
	if !t.IsZero() {
		c.UpdatedAt = t
		c.LastEventAt = t
	}
	if ev.Summary != nil {
		c.Summary = *ev.Summary
	}
	if ev.Owner != nil {
		c.OwnerSessionID = *ev.Owner
	}
	if ev.Reasoning != "" {
		c.Reasoning = ev.Reasoning
	}
	if ev.CreatedBy != "" {
		c.CreatedBy = ev.CreatedBy
	}
	if ev.Source != nil {
		c.Source = *ev.Source
	}

	state[id] = c
}

// statusFromRaw maps the plugin's write-path status string directly to
// the GraphQL enum. Legacy "blocked" events (v0.6) and any unknown
// future value fold as OPEN — only "delivered" closes a contract.
//
// This is the single mapping point from raw status string to typed
// enum. The internal Contract.Status field carries the enum value so
// downstream callers (matches, toGraphQL) never re-switch on strings.
func statusFromRaw(s string) graphql.ContractStatus {
	if s == "delivered" {
		return graphql.ContractStatusDelivered
	}
	return graphql.ContractStatusOpen
}

// eventTime returns the timestamp for an event. Returns zero when no
// timestamp is present.
func eventTime(ev Event) time.Time {
	if ev.Timestamp == nil {
		return time.Time{}
	}
	return *ev.Timestamp
}
