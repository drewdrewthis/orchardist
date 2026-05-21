package contracts

import (
	"time"
)

// ContractID is the cache key for the Contract provider. Equal to the
// plugin-issued contract id (e.g. "C-2026-05-04-abc12345").
type ContractID string

// Event is one line of the contract JSONL log. The plugin emits
// several event kinds — creation, status_change, criterion_added,
// question_asked, question_answered, cancel_requested, cancel_acked,
// cancel_withdrawn, judge_run, cooldown_set, wait_started, etc. —
// each with a `kind` discriminator. The shape is intentionally a
// single struct: most fields are pointers / optional, and Fold uses
// the discriminator to decide which fields to read.
//
// Field order mirrors the on-disk JSONL so that go-toolchain
// reflection and `cmp.Diff` output read like the file does.
type Event struct {
	// Kind discriminates which fields are present. Required on every
	// line. Unknown kinds are skipped by Fold so the daemon stays
	// forward-compatible with plugin extensions.
	Kind string `json:"kind"`

	// Timestamp is required on every event except the creation
	// record (which uses CreatedOn instead). RFC 3339 format.
	Timestamp *time.Time `json:"timestamp,omitempty"`

	// Creation events embed the full contract record inline.
	ID            string     `json:"id,omitempty"`
	Statement     string     `json:"statement,omitempty"`
	Owner         *Party     `json:"owner,omitempty"`
	ReportsTo     *Party     `json:"reports_to,omitempty"`
	Parent        *string    `json:"parent_contract_id,omitempty"`
	CreatedOn     *time.Time `json:"created_on,omitempty"`
	UpdatedOn     *time.Time `json:"updated_on,omitempty"`
	InitialStatus string     `json:"status,omitempty"` // creation status (always "open" today)

	// Status-change events.
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Trigger string `json:"trigger,omitempty"`

	// Criterion-added events.
	Criterion string `json:"criterion,omitempty"`
	Rationale string `json:"rationale,omitempty"`

	// Question events.
	QuestionID       string     `json:"question_id,omitempty"`
	QuestionText     string     `json:"text,omitempty"`
	QuestionBlocks   *bool      `json:"blocks_close,omitempty"`
	QuestionDeadline *time.Time `json:"deadline_timestamp,omitempty"`
	QuestionAskedTo  *Party     `json:"asked_to,omitempty"`
	QuestionAnswer   string     `json:"answer,omitempty"`

	// Cancel events.
	CancelRequestID string     `json:"cancel_request_id,omitempty"`
	CancelDeadline  *time.Time `json:"ack_deadline_timestamp,omitempty"`

	// Cooldown / wait events.
	UntilTimestamp *time.Time `json:"until_timestamp,omitempty"`

	// Judge-run events.
	Verdict       string   `json:"verdict,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	EvidenceLinks []string `json:"evidence_links,omitempty"`

	// By is the agent that produced the event. Present on most kinds;
	// absent on system-generated status_change rows where Trigger
	// carries the cause.
	By string `json:"by,omitempty"`
}

// Party identifies an agent or Drew. Matches the plugin's nested shape:
// `{kind: "drew" | "agent", agent_name?, vm_address?, session_id?}`.
type Party struct {
	Kind      string  `json:"kind,omitempty"`
	AgentName *string `json:"agent_name,omitempty"`
	SessionID string  `json:"session_id,omitempty"`
	VMAddress *string `json:"vm_address,omitempty"`
}

// Contract is the folded state of one contract, in the daemon's
// internal shape. The resolver layer projects this onto graphql.Contract.
//
// v0.8 two-status model:
//   - Status "open"   maps to ContractStatusSigned (active).
//   - Status "closed" maps to ContractStatusClosed (ended).
//   - ClosedReason "delivered" maps to ContractReasonDelivered.
//   - ClosedReason "abandoned" maps to ContractReasonAbandoned.
//   - ClosedReason is empty when Status is "open".
type Contract struct {
	ID             ContractID
	Statement      string
	OwnerSessionID string
	OwnerAgentName string
	Status         string // "open" | "closed"
	ClosedReason   string // "delivered" | "abandoned" | ""
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastEventAt    time.Time
}
