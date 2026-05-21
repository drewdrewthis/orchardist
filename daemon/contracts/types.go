// Package contracts implements the read-only contracts domain: agent delivery
// commitments parsed from the Contracts-plugin records that ride on the JSONL
// stream alongside Conversation records.
//
// State is a projection of JSONL event records — nothing is persisted (L9).
// The 9-status state machine (OPEN → ... → SATISFIED | CANCELLED |
// JUDGE_REJECTED_TERMINAL) lives entirely in [Fold].
//
// This domain CONSUMES the claude-jsonls service via the [ClaudeJSONLSReader]
// interface (R4) — it does not parse JSONL files directly.
package contracts

import "time"

// ContractID is the cache key. Equal to the plugin-issued contract id
// (e.g. "C-2026-05-04-abc12345").
type ContractID string

// ContractStatus is the folded lifecycle state of one contract.
type ContractStatus string

const (
	StatusOpen                           ContractStatus = "OPEN"
	StatusDeliveredPendingValidation     ContractStatus = "DELIVERED_PENDING_VALIDATION"
	StatusDeliveredPendingParentValidation ContractStatus = "DELIVERED_PENDING_PARENT_VALIDATION"
	StatusPendingUserApproval            ContractStatus = "PENDING_USER_APPROVAL"
	StatusAwaitingCancelAck              ContractStatus = "AWAITING_CANCEL_ACK"
	StatusWaitingExternal                ContractStatus = "WAITING_EXTERNAL"
	StatusSatisfied                      ContractStatus = "SATISFIED"
	StatusCancelled                      ContractStatus = "CANCELLED"
	StatusJudgeRejectedTerminal          ContractStatus = "JUDGE_REJECTED_TERMINAL"
)

// Contract is the folded state of one contract in the daemon's internal shape.
// The resolver layer projects this onto the GraphQL Contract type.
type Contract struct {
	ID               ContractID
	Statement        string
	OwnerSessionID   string
	OwnerAgentName   string
	ReportsTo        string // empty when unset; "drew" or "agent:<name>"
	ParentContractID string // empty when top-level
	Status           ContractStatus
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastEventAt      time.Time
	Criteria         []string
	OpenQuestions    []OpenQuestion
}

// OpenQuestion is a question still awaiting an answer.
// The fold drops answered/timed-out questions from this list.
type OpenQuestion struct {
	QuestionID  string
	Text        string
	AskedBy     string
	AskedAt     time.Time
	Deadline    *time.Time
	BlocksClose bool
}

// ContractFilter carries optional filter parameters for [ContractsService.List].
// All fields are ANDed; nil/empty fields match everything.
type ContractFilter struct {
	Statuses         []ContractStatus
	OwnerSessionID   *string
	OwnerAgentName   *string
	ParentContractID *string
}

// Event is one line of the contract JSONL log. The plugin emits several event
// kinds — creation, status_change, criterion_added, question_asked,
// question_answered, cancel_requested, etc. — each with a `kind`
// discriminator. The shape is intentionally a single struct: most fields are
// pointers/optional, and [Fold] uses the discriminator to decide which to read.
type Event struct {
	// Kind discriminates which fields are present. Required on every line.
	// Unknown kinds are skipped by Fold so the daemon stays forward-compatible.
	Kind string `json:"kind"`

	// Timestamp is required on every event except the creation record (which
	// uses CreatedOn). RFC 3339 format.
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

	// By is the agent that produced the event.
	By string `json:"by,omitempty"`
}

// Party identifies an agent or user. Matches the plugin's nested shape:
// `{kind: "drew" | "agent", agent_name?, vm_address?, session_id?}`.
type Party struct {
	Kind      string  `json:"kind,omitempty"`
	AgentName *string `json:"agent_name,omitempty"`
	SessionID string  `json:"session_id,omitempty"`
	VMAddress *string `json:"vm_address,omitempty"`
}

// InvalidationEvent signals that a cached contract's value may have changed.
type InvalidationEvent struct {
	Key    ContractID
	Reason string
	At     time.Time
}
