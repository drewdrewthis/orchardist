package contracts

import (
	"time"
)

// ContractID is the cache key for the Contract provider. Equal to the
// plugin-issued contract id (e.g. "C-2026-05-04-abc12345").
type ContractID string

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
	Status         string // "open" | "closed"
	ClosedReason   string // "delivered" | "abandoned" | ""
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastEventAt    time.Time
}
