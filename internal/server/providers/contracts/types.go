package contracts

import (
	"time"
)

// ContractID is the cache key for the Contract provider. Equal to the
// plugin-issued contract id (e.g. "C-2026-05-04-abc12345").
type ContractID string

// Event is one line of the v0.7 contract JSONL log. The v0.7 plugin
// uses a flat shape: every line has the same fields, no `kind`
// discriminator. The status field drives the fold.
//
// Legacy v0.6 lines that carry a `kind` field are handled by the fold:
// the `Kind` field is populated but ignored as a dispatch key — instead
// the fold reads `Status` directly and discards events where Status is
// empty (v0.6 non-status events like criterion_added, question_asked,
// etc.).
//
// Field order mirrors the on-disk JSONL shape so that go-toolchain
// reflection and `cmp.Diff` output read like the file does.
type Event struct {
	// Timestamp is required on every event. RFC 3339 format.
	Timestamp *time.Time `json:"timestamp,omitempty"`

	// ContractID identifies which contract this event belongs to.
	// The v0.7 plugin writes `contract_id`; the adapter populates this.
	ContractID string `json:"contract_id,omitempty"`

	// Status is the new folded status after this event. Valid write
	// values: "started" | "delivered". Legacy "blocked" events must
	// parse and fold as open. An empty Status causes the event to be
	// skipped by Fold (forward-compat and legacy v0.6 event kinds that
	// don't carry status).
	Status string `json:"status,omitempty"`

	// Summary is the human-readable contract statement. Set on the first
	// event (creation); subsequent events carry null and inherit from the
	// prior fold state.
	Summary *string `json:"summary,omitempty"`

	// Reasoning is updated on every event to the most-recent author's
	// rationale.
	Reasoning string `json:"reasoning,omitempty"`

	// Owner is the session id string in `machine:project:session_id`
	// form. Null on update events signals no owner change (inherit).
	Owner *string `json:"owner,omitempty"`

	// CreatedBy is the identity of the agent that wrote this event.
	CreatedBy string `json:"created_by,omitempty"`

	// Source is an optional commitment origin (issue:..., pr:...,
	// conversation:...). May be set on any event; the most recent
	// non-empty value wins in the fold.
	Source *string `json:"source,omitempty"`

	// Kind is present only on legacy v0.6 events. It is accepted in the
	// JSON decode so the struct does not reject those lines, but Fold
	// does not use it as a dispatch key — the Status field drives
	// everything.
	Kind string `json:"kind,omitempty"`
}

// Contract is the folded state of one contract, in the daemon's
// internal shape. The resolver layer projects this onto graphql.Contract.
type Contract struct {
	ID             ContractID
	Summary        string
	OwnerSessionID string
	OwnerAgentName string // deprecated: empty for v0.7; best-effort for v0.6
	Status         string // raw plugin status string; resolver maps to enum
	Reasoning      string
	CreatedBy      string
	Source         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastEventAt    time.Time
}
